package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
		name    string
		file    string
		env     map[string]string
		want    bool
		wantErr bool
	}{
		{name: "missing_option"},
		{name: "file_enabled", file: "ANTI_TRUNCATION=true\n", want: true},
		{name: "file_disabled", file: "ANTI_TRUNCATION=false\n"},
		{name: "trimmed_file", file: "ANTI_TRUNCATION=\" true \"\n", want: true},
		{name: "env_enables", file: "ANTI_TRUNCATION=false\n", env: map[string]string{"ANTI_TRUNCATION": "true"}, want: true},
		{name: "env_disables", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": "false"}},
		{name: "trimmed_env", env: map[string]string{"ANTI_TRUNCATION": " true "}, want: true},
		{name: "env_replaces_invalid_file", file: "ANTI_TRUNCATION=invalid\n", env: map[string]string{"ANTI_TRUNCATION": "true"}, want: true},
		{name: "invalid_file", file: "ANTI_TRUNCATION=invalid\n", wantErr: true},
		{name: "empty_file_value", file: "ANTI_TRUNCATION=\n", wantErr: true},
		{name: "invalid_env", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": "invalid"}, wantErr: true},
		{name: "empty_env", file: "ANTI_TRUNCATION=true\n", env: map[string]string{"ANTI_TRUNCATION": ""}, wantErr: true},
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
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "ANTI_TRUNCATION") {
					t.Fatalf("expected ANTI_TRUNCATION validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := Default()
			want.AntiTruncation = test.want
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
				values, err := readEnvFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if values["ANTI_TRUNCATION"] != strconv.FormatBool(enabled) {
					t.Fatalf("saved ANTI_TRUNCATION = %q, want %t", values["ANTI_TRUNCATION"], enabled)
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
