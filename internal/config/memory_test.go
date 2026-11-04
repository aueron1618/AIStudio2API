package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestMinimumMemoryDefaults(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.WAABackend != WAABackendSpiderMonkey || cfg.WarmWorkerLimit != 0 || cfg.MaxActiveWorkers != 1 || cfg.WarmStartupConcurrency != 1 || cfg.PerAccountConcurrency != 1 {
		t.Fatalf("not minimum-memory defaults: %+v", cfg)
	}
	if !slices.Equal(cfg.UpstreamChannels, []string{"playground", "build"}) {
		t.Fatalf("default channels changed: %v", cfg.UpstreamChannels)
	}
	for _, backend := range []string{WAABackendSpiderMonkey, WAABackendCamoufox} {
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
	if loaded.WarmWorkerLimit != 0 || loaded.MaxActiveWorkers != 1 || loaded.WAABackend != WAABackendSpiderMonkey {
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

func TestLegacyGoBackendMigratesWithoutOverwritingCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("WAA_BACKEND=go\nWARM_WORKER_LIMIT=2\nMAX_ACTIVE_WORKERS=4\nWARM_STARTUP_CONCURRENCY=3\nPER_ACCOUNT_CONCURRENCY=2\nREQUEST_BODY_LOG=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WAABackend != WAABackendSpiderMonkey || cfg.WarmWorkerLimit != 2 || cfg.MaxActiveWorkers != 4 || cfg.WarmStartupConcurrency != 3 || cfg.PerAccountConcurrency != 2 || !cfg.RequestBodyLog {
		t.Fatalf("existing deployment settings changed: %+v", cfg)
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

func TestWarmWorkerEnvironmentAllowsZeroAndRejectsNegative(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	t.Setenv("MAX_ACTIVE_WORKERS", "4")
	for _, value := range []string{"0", "2", "-1", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WARM_WORKER_LIMIT", value)
			cfg, err := Load(path)
			if err != nil || cfg.MaxActiveWorkers != 4 {
				t.Fatalf("warm limit changed an explicit active limit: %+v, %v", cfg, err)
			}
			want := 0
			if value == "2" {
				want = 2
			}
			if cfg.WarmWorkerLimit != want {
				t.Fatalf("warm limit %q = %d, want %d", value, cfg.WarmWorkerLimit, want)
			}
			invalid := value == "-1" || value == "invalid"
			if (len(cfg.Invalid) != 0) != invalid {
				t.Fatalf("warm limit %q invalid notices: %+v", value, cfg.Invalid)
			}
		})
	}
}
