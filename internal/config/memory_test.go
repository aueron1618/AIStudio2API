package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMinimumMemoryDefaults(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.WAABackend != WAABackendGo || cfg.WarmWorkerLimit != 0 || cfg.MaxActiveWorkers != 1 || cfg.WarmStartupConcurrency != 1 || cfg.PerAccountConcurrency != 1 {
		t.Fatalf("not minimum-memory defaults: %+v", cfg)
	}
	for _, backend := range []string{WAABackendGo, WAABackendCamoufox} {
		cfg.WAABackend = backend
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Config
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.WarmWorkerLimit != 0 || decoded.WAABackend != backend {
			t.Fatal("JSON round trip changed zero warm pool")
		}
	}
}

func TestZeroWarmPoolSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WarmWorkerLimit != 0 || loaded.MaxActiveWorkers != 1 || loaded.WAABackend != WAABackendGo {
		t.Fatal("save/load changed defaults")
	}
	// Explicit legacy settings remain authoritative.
	t.Setenv("WAA_BACKEND", WAABackendCamoufox)
	t.Setenv("WARM_WORKER_LIMIT", "5")
	t.Setenv("MAX_ACTIVE_WORKERS", "10")
	loaded, err = Load(path)
	if err != nil || loaded.WAABackend != WAABackendCamoufox || loaded.WarmWorkerLimit != 5 {
		t.Fatalf("explicit configuration lost: %+v, %v", loaded, err)
	}
}

func TestWorkerLimitValidation(t *testing.T) {
	for _, test := range []struct {
		warm, active, startup int
		valid                 bool
	}{
		{0, 1, 1, true}, {1, 1, 1, true}, {0, 0, 1, false}, {-1, 1, 1, false}, {2, 1, 1, false}, {0, 1, 2, false},
	} {
		cfg := Default()
		cfg.WarmWorkerLimit, cfg.MaxActiveWorkers, cfg.WarmStartupConcurrency = test.warm, test.active, test.startup
		if err := cfg.Validate(); (err == nil) != test.valid {
			t.Fatalf("limits %+v: %v", test, err)
		}
	}
}
