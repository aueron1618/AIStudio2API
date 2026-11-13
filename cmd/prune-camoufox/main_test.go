package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

func writeFixture(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func collectionFixture(fonts ...[]byte) []byte {
	data := make([]byte, 12+4*len(fonts))
	copy(data, "ttcf")
	binary.BigEndian.PutUint32(data[4:], 0x00010000)
	binary.BigEndian.PutUint32(data[8:], uint32(len(fonts)))
	for index, original := range fonts {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		offset := uint32(len(data))
		binary.BigEndian.PutUint32(data[12+index*4:], offset)
		font := bytes.Clone(original)
		for index := range int(binary.BigEndian.Uint16(font[4:])) {
			entry := font[12+index*16:]
			binary.BigEndian.PutUint32(entry[8:], binary.BigEndian.Uint32(entry[8:])+offset)
		}
		data = append(data, font...)
	}
	return data
}

func bundleFixture(t *testing.T) (string, []string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "browser & fonts")
	for name, data := range map[string][]byte{
		"camoufox-bin":                  []byte("browser executable"),
		"libxul.so":                     []byte("browser library"),
		"fontconfig/windows/fonts.conf": []byte(`<fontconfig><dir prefix="cwd">fonts</dir></fontconfig>`),
		"fontconfig/linux/fonts.conf":   []byte("linux configuration"),
		"fontconfig/macos/fonts.conf":   []byte("macOS configuration"),
		"fonts/groups.json":             []byte(`{"groups":["L","LM","LMW","LW","M","MW","W"],"readBy":{"win":["LMW","LW","MW","W"],"lin":["L","LM","LMW","LW"],"mac":["LM","LMW","M","MW"]}}`),
		"fonts/TwemojiMozilla.ttf":      goregular.TTF,
		"fonts/L/linux.ttf":             goregular.TTF,
		"fonts/LM/shared.ttf":           goregular.TTF,
		"fonts/M/macos.ttf":             goregular.TTF,
		"fonts/LMW/regular.ttf":         goregular.TTF,
		"fonts/MW/bold.ttf":             gobold.TTF,
		"fonts/LW/unneeded.ttf":         gomono.TTF,
		"fonts/W/collection.ttc":        collectionFixture(gomono.TTF, goregular.TTF),
		"fonts/W/unknown.otf":           []byte("future font format"),
	} {
		writeFixture(t, root, name, data)
	}
	inputs := t.TempDir()
	writeFixture(t, inputs, "profile.json", []byte(`{"fonts":["Go"]}`))
	writeFixture(t, inputs, "data.json", []byte(`{}`))
	return root, []string{filepath.Join(inputs, "profile.json"), filepath.Join(inputs, "data.json")}
}

func snapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil {
			result[path] = sha256.Sum256(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPruneFontsPreservesUsedFaces(t *testing.T) {
	root, profiles := bundleFixture(t)
	before := snapshot(t, root)
	result, err := pruneFonts(root, profiles, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedFiles != 4 || result.KeptFiles != 5 || result.UnknownFiles != 1 || result.RemovedBytes == 0 {
		t.Fatalf("unexpected pruning result: %+v", result)
	}
	for _, name := range []string{"fonts/L", "fonts/LM", "fonts/M", "fonts/LW", "fontconfig/linux", "fontconfig/macos"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unused resource was not removed: %s (%v)", name, err)
		}
	}
	for path, hash := range snapshot(t, root) {
		if original, ok := before[path]; ok && path != filepath.Join(root, "fonts", "groups.json") && hash != original {
			t.Fatalf("retained resource was rewritten: %s", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "fonts", "groups.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fontGroups
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(manifest.Groups, []string{"LMW", "MW", "W"}) || !slices.Equal(manifest.ReadBy["win"], manifest.Groups) || len(manifest.ReadBy) != 1 {
		t.Fatalf("manifest references removed groups: %+v", manifest)
	}
	checksums, err := os.ReadFile(filepath.Join(root, "fonts.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(checksums)), "\n")
	if len(lines) != result.KeptFiles+2 {
		t.Fatalf("incomplete checksum manifest: %s", checksums)
	}
	for _, line := range lines {
		digest, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("invalid checksum line: %q", line)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if digest != hex.EncodeToString(hash[:]) {
			t.Fatalf("incorrect checksum for %s", name)
		}
	}
}

func TestPruneFontsIncludesAliasesAndFallbacks(t *testing.T) {
	for _, metadata := range []string{
		`{"families":[["Go","Go Mono"]]}`,
		`{"substitutes":{"Custom Alias":"Go Mono"}}`,
		`{"prefs":{"font.name.monospace.ja":"Go Mono"}}`,
		`{"prefs":{"font.name-list.serif.zh-CN":"Missing, Go Mono"}}`,
		`{"prefs":{"font.name.monospace.x-western":"  gO   mOnO  "}}`,
	} {
		t.Run(metadata, func(t *testing.T) {
			root, profiles := bundleFixture(t)
			if err := os.WriteFile(profiles[1], []byte(metadata), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := pruneFonts(root, profiles, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "fonts", "LW", "unneeded.ttf")); err != nil {
				t.Fatalf("font used by an alias or locale fallback was removed: %v", err)
			}
		})
	}
}

func TestPruneFontsDryRunAndPreflight(t *testing.T) {
	for _, name := range []string{"dry run", "empty profile", "invalid group", "missing group", "empty group", "missing template", "no matching fonts"} {
		t.Run(name, func(t *testing.T) {
			root, profiles := bundleFixture(t)
			switch name {
			case "empty profile":
				writeFixture(t, filepath.Dir(profiles[0]), filepath.Base(profiles[0]), []byte(`{}`))
			case "invalid group":
				writeFixture(t, root, "fonts/groups.json", []byte(`{"groups":["../outside"],"readBy":{"win":["../outside"]}}`))
			case "missing group", "empty group":
				directory := filepath.Join(root, "fonts", "W")
				if err := os.RemoveAll(directory); err != nil {
					t.Fatal(err)
				}
				if name == "empty group" {
					if err := os.Mkdir(directory, 0o755); err != nil {
						t.Fatal(err)
					}
				}
			case "missing template":
				if err := os.Remove(filepath.Join(root, "fontconfig", "windows", "fonts.conf")); err != nil {
					t.Fatal(err)
				}
			case "no matching fonts":
				writeFixture(t, filepath.Dir(profiles[0]), filepath.Base(profiles[0]), []byte(`{"fonts":["Not Installed"]}`))
			}
			before := snapshot(t, root)
			_, err := pruneFonts(root, profiles, name == "dry run")
			if (err == nil) != (name == "dry run") {
				t.Fatalf("unexpected preflight result: %v", err)
			}
			if !maps.Equal(before, snapshot(t, root)) {
				t.Fatal("dry run or failed preflight modified the bundle")
			}
		})
	}
}

func TestFontNamesAllCollectionFacesAndLocales(t *testing.T) {
	names, err := fontNames(bytes.NewReader(collectionFixture(gomono.TTF, goregular.TTF)))
	if err != nil || !slices.Contains(names, "Go") || !slices.Contains(names, "Go Mono") {
		t.Fatalf("collection names=%v, error=%v", names, err)
	}
	// A minimal name-only font exercises a non-English record, not just the preferred name.
	families := []string{"Unused English Name", "\u6e38\u30b4\u30b7\u30c3\u30af"}
	table := make([]byte, 6+12*len(families))
	binary.BigEndian.PutUint16(table[2:], uint16(len(families)))
	binary.BigEndian.PutUint16(table[4:], uint16(len(table)))
	for index, family := range families {
		record := table[6+12*index:]
		binary.BigEndian.PutUint16(record, 3)
		binary.BigEndian.PutUint16(record[2:], 1)
		binary.BigEndian.PutUint16(record[4:], uint16(0x0409+index))
		binary.BigEndian.PutUint16(record[6:], 1)
		units := utf16.Encode([]rune(family))
		binary.BigEndian.PutUint16(record[8:], uint16(len(units)*2))
		binary.BigEndian.PutUint16(record[10:], uint16(len(table)-(6+12*len(families))))
		for _, unit := range units {
			table = binary.BigEndian.AppendUint16(table, unit)
		}
	}
	font := make([]byte, 28)
	binary.BigEndian.PutUint32(font, 0x00010000)
	binary.BigEndian.PutUint16(font[4:], 1)
	copy(font[12:], "name")
	binary.BigEndian.PutUint32(font[20:], uint32(len(font)))
	binary.BigEndian.PutUint32(font[24:], uint32(len(table)))
	names, err = fontNames(bytes.NewReader(append(font, table...)))
	if err != nil || !slices.Equal(names, families) {
		t.Fatalf("localized names=%v, error=%v", names, err)
	}
	for _, data := range [][]byte{nil, []byte("unknown format"), []byte("ttcf\x00\x01\x00\x00\xff\xff\xff\xff")} {
		if _, err := fontNames(bytes.NewReader(data)); err == nil {
			t.Fatal("malformed font metadata was accepted")
		}
	}
}
