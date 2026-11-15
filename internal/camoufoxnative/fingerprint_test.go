package camoufoxnative

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAccountFingerprintPersistsWithoutBrowser(t *testing.T) {
	for _, locale := range []string{"en-US", "zh-CN", "ja-JP", "fr-FR"} {
		t.Run(locale, func(t *testing.T) {
			options := Options{StorageStatePath: filepath.Join(t.TempDir(), "storage-state.json"), Locale: locale, Timezone: "UTC"}
			first, err := AccountFingerprint(options)
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			second, err := AccountFingerprint(options)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(second)
			if err != nil || string(before) != string(after) {
				t.Fatalf("account fingerprint changed on reuse: %v", err)
			}
			if !windowsFonts(first["fonts"], locale) {
				t.Fatal("fingerprint did not use Windows/locale fonts")
			}
			for _, key := range []string{"canvas:seed", "audio:seed", "fonts:spacing_seed", "navigator.maxTouchPoints", "screen.availTop", "screen.availLeft"} {
				if _, found := first[key]; found {
					t.Fatalf("removed Firefox 158 setting retained: %s", key)
				}
			}
			for _, raw := range second["voices"].([]any) {
				voice := raw.(map[string]any)
				if voice["isDefault"] != false || voice["voiceUri"] != "urn:moz-tts:sapi:"+voice["name"].(string)+"?"+voice["lang"].(string) {
					t.Fatalf("invalid Firefox 158 voice: %+v", voice)
				}
			}
		})
	}
}

func TestHardwareDistributionsKeepCoherentWindowsGeometry(t *testing.T) {
	for range 100 {
		config := map[string]any{}
		applyWindowsDisplay(config)
		normalizeCamoufoxConfig(config)
		width, _ := integer(config, "screen.width")
		height, _ := integer(config, "screen.height")
		availHeight, _ := integer(config, "screen.availHeight")
		if width <= 0 || height <= 0 || availHeight != height-firefoxTaskbar {
			t.Fatalf("invalid Firefox display: %+v", config)
		}
		for axis, position := range map[string]string{"Width": "window.screenX", "Height": "window.screenY"} {
			inner, _ := integer(config, "window.inner"+axis)
			outer, _ := integer(config, "window.outer"+axis)
			screen, _ := integer(config, "screen."+strings.ToLower(axis))
			at, _ := integer(config, position)
			border := windowBorder(config)
			if inner <= 0 || inner > outer || outer > screen+2*border || at < -border || at+outer > screen+border {
				t.Fatalf("incoherent %s geometry: %+v", axis, config)
			}
		}
		if !slices.Contains([]int{2, 4, 6, 8, 12, 16, 20, 22, 24, 32}, firefoxHardwareConcurrency()) {
			t.Fatal("CPU outside the upstream distribution")
		}
		if !strings.HasSuffix(config["webGl:renderer"].(string), ", or similar") {
			t.Fatal("WebGL renderer lost Firefox 158 normalization")
		}
	}
}
