package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAntiTruncationConfig(t *testing.T) {
	// Keep file/default cases independent of the host's process configuration.
	for _, key := range configKeys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("default", func(t *testing.T) {
		want := Default()
		if want.AntiTruncation {
			t.Fatal("anti-truncation must be disabled by default")
		}
		got, err := Load(filepath.Join(t.TempDir(), ".env"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("missing config changed defaults: got %+v, want %+v", got, want)
		}
	})

	for _, test := range []struct {
		name        string
		file        string
		env         map[string]string
		want        bool
		wantInvalid bool
	}{
		{name: "missing_option"},
		{name: "file_enabled", file: "ANTI_TRUNCATION=true\n", want: true},
		{name: "file_disabled", file: "ANTI_TRUNCATION=false\n"},
		{name: "trimmed_file", file: "ANTI_TRUNCATION=\" true \"\n", want: true},
		{name: "env_enables", file: "ANTI_TRUNCATION=false\n", env: map[string]string{"ANTI_TRUNCATION": "true"}, want: true},
		{name: "env_disables", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": "false"}},
		{name: "trimmed_env", env: map[string]string{"ANTI_TRUNCATION": " true "}, want: true},
		{name: "env_replaces_invalid_file", file: "ANTI_TRUNCATION=invalid\n", env: map[string]string{"ANTI_TRUNCATION": "true"}, want: true},
		{name: "invalid_file", file: "ANTI_TRUNCATION=invalid\n", wantInvalid: true},
		{name: "empty_file_value", file: "ANTI_TRUNCATION=\n", wantInvalid: true},
		{name: "invalid_env", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": "invalid"}, wantInvalid: true},
		{name: "empty_env", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": ""}, wantInvalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(test.file), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			want := Default()
			want.AntiTruncation = test.want
			if test.wantInvalid {
				if len(got.Invalid) != 1 || got.Invalid[0].Key != "ANTI_TRUNCATION" || !strings.Contains(got.Invalid[0].Reason, "ANTI_TRUNCATION") {
					t.Fatalf("expected ANTI_TRUNCATION invalid-value notice, got %+v", got.Invalid)
				}
				want.Invalid = got.Invalid
			}
			if len(test.env) != 0 {
				want.Environment = []string{"ANTI_TRUNCATION"}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Load() = %+v, want %+v", got, want)
			}
		})
	}

	t.Run("existing_config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("AUTO_CONTINUATION=false\nCONTINUATION_REWRITE=false\nMODEL_MAPPING=custom:gemini-test\nGOOGLE_SEARCH=true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := Default()
		want.AutoContinuation = false
		want.ContinuationRewrite = false
		want.ModelMapping = "custom:gemini-test"
		want.GoogleSearch = true
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("existing configuration changed: got %+v, want %+v", got, want)
		}
	})

	t.Run("save_load", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		for _, enabled := range []bool{true, false} {
			for _, continuation := range []bool{true, false} {
				want := Default()
				want.AntiTruncation = enabled
				want.AutoContinuation = continuation
				if err := want.Save(path); err != nil {
					t.Fatal(err)
				}
				values, _, err := readEnvFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if value, exists := values["ANTI_TRUNCATION"]; exists != enabled || enabled && value != "true" {
					t.Fatalf("saved ANTI_TRUNCATION = %q, exists=%t, enabled=%t", value, exists, enabled)
				}
				got, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("save/load changed configuration: got %+v, want %+v", got, want)
				}
			}
		}
	})
}
