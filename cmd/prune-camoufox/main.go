package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

type fontGroups struct {
	Groups []string            `json:"groups"`
	ReadBy map[string][]string `json:"readBy"`
}

type pruneResult struct {
	KeptFiles, RemovedFiles, UnknownFiles int
	KeptBytes, RemovedBytes               int64
}

func normalizedFontName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), ""))
}

func main() {
	root := flag.String("root", "", "unpacked Camoufox directory")
	profile := flag.String("profile", "", "Windows fingerprint profile JSON")
	fontData := flag.String("font-data", "", "Windows font aliases and preferences JSON")
	dryRun := flag.Bool("dry-run", false, "report without changing any files")
	flag.Parse()
	if *root == "" || *profile == "" || *fontData == "" {
		flag.Usage()
		os.Exit(2)
	}
	result, err := pruneFonts(*root, []string{*profile, *fontData}, *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Camoufox fonts: keep %d files (%.2f MiB), remove %d files (%.2f MiB), conservatively keep %d unrecognized fonts; dry-run=%t\n",
		result.KeptFiles, float64(result.KeptBytes)/(1<<20), result.RemovedFiles, float64(result.RemovedBytes)/(1<<20), result.UnknownFiles, *dryRun)
}

func pruneFonts(root string, profiles []string, dryRun bool) (pruneResult, error) {
	var result pruneResult
	wanted := map[string]bool{}
	add := func(name string) { wanted[normalizedFontName(name)] = true }
	for _, path := range profiles {
		data, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		var profile struct {
			Fonts       []string                   `json:"fonts"`
			Families    [][]string                 `json:"families"`
			Substitutes map[string]string          `json:"substitutes"`
			Prefs       map[string]json.RawMessage `json:"prefs"`
		}
		if err := json.Unmarshal(data, &profile); err != nil {
			return result, fmt.Errorf("read font profile %s: %w", path, err)
		}
		for _, name := range profile.Fonts {
			add(name)
		}
		for _, family := range profile.Families {
			for _, name := range family {
				add(name)
			}
		}
		for alias, target := range profile.Substitutes {
			add(alias)
			add(target)
		}
		for key, value := range profile.Prefs {
			if !strings.HasPrefix(key, "font.name.") && !strings.HasPrefix(key, "font.name-list.") {
				continue
			}
			var names string
			if err := json.Unmarshal(value, &names); err != nil {
				return result, fmt.Errorf("invalid font preference %s: %w", key, err)
			}
			for name := range strings.SplitSeq(names, ",") {
				add(name)
			}
		}
	}
	delete(wanted, "")
	if len(wanted) == 0 {
		return result, errors.New("font profiles contain no font names")
	}
	fontRoot := filepath.Join(root, "fonts")
	if _, err := os.ReadFile(filepath.Join(root, "fontconfig", "windows", "fonts.conf")); err != nil {
		return result, fmt.Errorf("read Windows fontconfig template: %w", err)
	}
	manifestPath := filepath.Join(fontRoot, "groups.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return result, err
	}
	var manifest fontGroups
	if err := json.Unmarshal(data, &manifest); err != nil {
		return result, err
	}
	if len(manifest.Groups) == 0 || len(manifest.ReadBy["win"]) == 0 {
		return result, errors.New("font manifest contains no Windows groups")
	}
	for _, group := range slices.Concat(manifest.Groups, manifest.ReadBy["win"]) {
		if !filepath.IsLocal(group) || group == "." || strings.ContainsAny(group, `/\`) {
			return result, fmt.Errorf("invalid font group %q", group)
		}
	}
	for _, group := range manifest.ReadBy["win"] {
		if !slices.Contains(manifest.Groups, group) {
			return result, fmt.Errorf("Windows font group %q is not declared", group)
		}
		entries, err := os.ReadDir(filepath.Join(fontRoot, group))
		if err != nil {
			return result, fmt.Errorf("read original Windows font group %q: %w", group, err)
		}
		if len(entries) == 0 {
			return result, fmt.Errorf("original Windows font group %q is empty", group)
		}
	}
	var kept, remove []string
	keptGroups := map[string]bool{}
	matched := 0
	err = filepath.WalkDir(fontRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink in font bundle: %s", path)
		}
		if entry.IsDir() || path == manifestPath {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected non-regular font resource: %s", path)
		}
		relative, err := filepath.Rel(fontRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.ContainsAny(relative, "\\\r\n") {
			return fmt.Errorf("unsupported checksum filename %q", relative)
		}
		group, _, grouped := strings.Cut(relative, "/")
		keep := !grouped || slices.Contains(manifest.ReadBy["win"], group)
		if grouped && keep {
			switch strings.ToLower(filepath.Ext(path)) {
			case ".ttf", ".otf", ".ttc", ".otc":
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				names, parseErr := fontNames(file)
				file.Close()
				if parseErr != nil {
					// Unknown formats stay intact; inability to inspect is not permission to delete.
					result.UnknownFiles++
				} else {
					keep = slices.ContainsFunc(names, func(name string) bool { return wanted[normalizedFontName(name)] })
					if keep {
						matched++
					}
				}
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if keep {
			kept = append(kept, path)
			result.KeptFiles++
			result.KeptBytes += info.Size()
			if grouped {
				keptGroups[group] = true
			}
		} else {
			remove = append(remove, path)
			result.RemovedFiles++
			result.RemovedBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if matched == 0 {
		return result, errors.New("no bundled Windows font matches the supplied profiles")
	}
	groups := slices.DeleteFunc(slices.Clone(manifest.ReadBy["win"]), func(group string) bool { return !keptGroups[group] })
	if dryRun {
		return result, nil
	}
	// Plan completely before deleting, and retain whole files whenever any TTC face is used.
	for _, path := range remove {
		if err := os.Remove(path); err != nil {
			return result, err
		}
	}
	for _, group := range manifest.Groups {
		if !slices.Contains(groups, group) {
			if err := os.RemoveAll(filepath.Join(fontRoot, group)); err != nil {
				return result, err
			}
		}
	}
	manifest.Groups, manifest.ReadBy = groups, map[string][]string{"win": groups}
	data, err = json.MarshalIndent(manifest, "", " ")
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return result, err
	}
	for _, platform := range []string{"linux", "macos"} {
		if err := os.RemoveAll(filepath.Join(root, "fontconfig", platform)); err != nil {
			return result, err
		}
	}
	kept = append(kept, manifestPath, filepath.Join(root, "fontconfig", "windows", "fonts.conf"))
	slices.Sort(kept)
	var checksums strings.Builder
	for _, path := range kept {
		file, err := os.Open(path)
		if err != nil {
			return result, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		file.Close()
		if copyErr != nil {
			return result, copyErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return result, err
		}
		fmt.Fprintf(&checksums, "%x  %s\n", hash.Sum(nil), filepath.ToSlash(relative))
	}
	err = os.WriteFile(filepath.Join(root, "fonts.sha256"), []byte(checksums.String()), 0o644)
	return result, err
}

// fontNames reads all localized family/full names without decoding or changing glyphs.
func fontNames(file io.ReaderAt) ([]string, error) {
	read := func(offset int64, size int) ([]byte, error) {
		data := make([]byte, size)
		_, err := file.ReadAt(data, offset)
		return data, err
	}
	header, err := read(0, 12)
	if err != nil {
		return nil, err
	}
	offsets := []uint32{0}
	if string(header[:4]) == "ttcf" {
		count := binary.BigEndian.Uint32(header[8:])
		if count == 0 || count > 4096 {
			return nil, errors.New("invalid font collection count")
		}
		data, err := read(12, int(count)*4)
		if err != nil {
			return nil, err
		}
		offsets = make([]uint32, count)
		for index := range offsets {
			offsets[index] = binary.BigEndian.Uint32(data[index*4:])
		}
	}
	var names []string
	for _, offset := range offsets {
		header, err := read(int64(offset), 12)
		if err != nil {
			return nil, err
		}
		switch string(header[:4]) {
		case "\x00\x01\x00\x00", "OTTO", "true":
		default:
			return nil, errors.New("unsupported font format")
		}
		directory, err := read(int64(offset)+12, int(binary.BigEndian.Uint16(header[4:]))*16)
		if err != nil {
			return nil, err
		}
		found := false
		for index := 0; index < len(directory); index += 16 {
			entry := directory[index : index+16]
			if string(entry[:4]) != "name" {
				continue
			}
			size := binary.BigEndian.Uint32(entry[12:])
			if size < 6 || size > 8<<20 {
				return nil, errors.New("invalid font name table size")
			}
			table, err := read(int64(binary.BigEndian.Uint32(entry[8:])), int(size))
			if err != nil {
				return nil, err
			}
			count, storage := int(binary.BigEndian.Uint16(table[2:])), int(binary.BigEndian.Uint16(table[4:]))
			if storage < 6+count*12 || storage > len(table) {
				return nil, errors.New("invalid font name records")
			}
			for recordIndex := range count {
				record := table[6+recordIndex*12 : 6+(recordIndex+1)*12]
				id := binary.BigEndian.Uint16(record[6:])
				if id != 1 && id != 4 && id != 6 && id != 16 && id != 21 {
					continue
				}
				start := storage + int(binary.BigEndian.Uint16(record[10:]))
				end := start + int(binary.BigEndian.Uint16(record[8:]))
				if end > len(table) {
					return nil, errors.New("invalid font name string")
				}
				raw := table[start:end]
				var name string
				switch binary.BigEndian.Uint16(record) {
				case 0, 3:
					if len(raw)%2 != 0 {
						return nil, errors.New("invalid UTF-16 font name")
					}
					units := make([]uint16, len(raw)/2)
					for index := range units {
						units[index] = binary.BigEndian.Uint16(raw[index*2:])
					}
					name = string(utf16.Decode(units))
				case 1:
					if binary.BigEndian.Uint16(record[2:]) != 0 {
						return nil, errors.New("unsupported legacy font name encoding")
					}
					name, err = charmap.Macintosh.NewDecoder().String(string(raw))
					if err != nil {
						return nil, err
					}
				default:
					return nil, errors.New("unsupported font name platform")
				}
				names = append(names, name)
				found = true
			}
		}
		if !found {
			return nil, errors.New("font face contains no supported names")
		}
	}
	return names, nil
}
