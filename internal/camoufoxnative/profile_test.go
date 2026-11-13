package camoufoxnative

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// profileProcessEnv 让测试二进制作为使用 profile 的进程运行：parent 启动一个 child 后报告就绪，两者运行 30 秒后写出 exited
const profileProcessEnv = "CAMOUFOX_PROFILE_PROCESS"

// TestMain 在设置了 profileProcessEnv 时作为测试用的进程运行，否则运行测试
func TestMain(m *testing.M) {
	switch os.Getenv(profileProcessEnv) {
	case "":
		os.Exit(m.Run())
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), profileProcessEnv+"=child")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		fmt.Println("ready")
	}
	time.Sleep(30 * time.Second)
	fmt.Println("exited")
	os.Exit(0)
}

// TestRemoveStaleProfilesEndsProfileProcesses 确认遗留 profile 删除前，以 -profile 引用它的进程连同子进程被结束
func TestRemoveStaleProfilesEndsProfileProcesses(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	profile, lock, err := createProfile()
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-profile", profile)
	command.Env = append(os.Environ(), profileProcessEnv+"=parent")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	output := bufio.NewReader(stdout)
	if line, err := output.ReadString('\n'); line != "ready\n" {
		t.Fatalf("使用 profile 的进程没有就绪: %q, %v", line, err)
	}
	removed, err := RemoveStaleProfiles()
	if removed != 1 || err != nil {
		t.Fatalf("RemoveStaleProfiles() = %d, %v，期望删除 1 个 profile", removed, err)
	}
	rest, err := io.ReadAll(output)
	if len(rest) != 0 || err != nil {
		t.Fatalf("使用 profile 的进程或其子进程运行到了结尾: %q, %v", rest, err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("使用 profile 的进程正常退出，没有被结束")
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile 仍然存在: %v", err)
	}
}

func TestWindowsFontconfigGroupedFonts(t *testing.T) {
	const manifest = `{"readBy":{"win":["LMW","LW","MW","W"],"lin":["L","LM","LMW","LW"],"mac":["LM","LMW","M","MW"]}}`
	const template = `<fontconfig><dir prefix="cwd">fonts</dir><match target="pattern"><edit name="hintstyle" mode="assign"><const>hintfull</const></edit></match></fontconfig>`
	for _, test := range []struct {
		name, manifest, template string
		missingGroup, emptyGroup string
		wantError                bool
	}{
		{name: "grouped", manifest: manifest, template: template},
		{name: "missing manifest", template: template, wantError: true},
		{name: "invalid manifest", manifest: "{", template: template, wantError: true},
		{name: "missing Windows groups", manifest: `{"readBy":{"lin":["L"]}}`, template: template, wantError: true},
		{name: "unsafe group", manifest: `{"readBy":{"win":["../M"]}}`, template: template, wantError: true},
		{name: "missing group", manifest: manifest, template: template, missingGroup: "MW", wantError: true},
		{name: "empty group", manifest: manifest, template: template, emptyGroup: "W", wantError: true},
		{name: "missing template", manifest: manifest, wantError: true},
		{name: "unsupported template", manifest: manifest, template: `<fontconfig><dir>/usr/share/fonts</dir></fontconfig>`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			browser := filepath.Join(t.TempDir(), "browser & fonts")
			profile := t.TempDir()
			for _, group := range []string{"L", "LM", "LMW", "LW", "M", "MW", "W"} {
				if group == test.missingGroup {
					continue
				}
				directory := filepath.Join(browser, "fonts", group)
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
				if group != test.emptyGroup {
					if err := os.WriteFile(filepath.Join(directory, "fixture.ttf"), []byte("font fixture"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if test.manifest != "" {
				if err := os.WriteFile(filepath.Join(browser, "fonts", "groups.json"), []byte(test.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			templatePath := filepath.Join(browser, "fontconfig", "windows", "fonts.conf")
			if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.template != "" {
				if err := os.WriteFile(templatePath, []byte(test.template), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path, err := windowsFontconfig(filepath.Join(browser, "camoufox-bin"), profile)
			if test.wantError {
				if err == nil {
					t.Fatal("invalid font resources were accepted")
				}
				if _, err := os.Stat(filepath.Join(profile, "fonts.conf")); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("failed preparation wrote a font config: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if path != filepath.Join(profile, "fonts.conf") {
				t.Fatalf("configuration was written outside the profile: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Directories []string `xml:"dir"`
			}
			if err := xml.Unmarshal(data, &config); err != nil {
				t.Fatalf("font config is not valid XML: %v", err)
			}
			var expected []string
			for _, group := range []string{"LMW", "LW", "MW", "W"} {
				expected = append(expected, filepath.Join(browser, "fonts", group))
			}
			if !slices.Equal(config.Directories, expected) {
				t.Fatalf("font directories=%v, want only Windows groups %v", config.Directories, expected)
			}
			content := string(data)
			if strings.Contains(content, `<const>hintfull</const>`) || !strings.Contains(content, `<const>hintslight</const>`) || !strings.Contains(content, `<family>fantasy</family><prefer><family>Arial</family>`) {
				t.Fatal("Windows rendering preferences or fantasy alias were lost")
			}
			for alias, substitute := range windowsFontData.Substitutes {
				if !strings.Contains(content, "<string>"+alias+"</string>") || !strings.Contains(content, "<string>"+substitute+"</string>") {
					t.Fatalf("missing Windows font substitution: %s -> %s", alias, substitute)
				}
			}
			original, err := os.ReadFile(templatePath)
			if err != nil || string(original) != test.template {
				t.Fatalf("bundled font config was modified: %v", err)
			}
		})
	}
}
