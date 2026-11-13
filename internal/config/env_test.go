package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeTestEnv 在临时目录写入 .env 并返回路径
func writeTestEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readTestEnv 读取 .env 全文
func readTestEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSaveWritesOnlyChangedKeys 核对保存只写与默认值不同的键，未写的键取当前默认值
func TestSaveWritesOnlyChangedKeys(t *testing.T) {
	path := writeTestEnv(t, "")
	cfg := Default()
	cfg.RoutingStrategy = "fill-first"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readTestEnv(t, path); got != "ROUTING_STRATEGY=fill-first\n" {
		t.Fatalf(".env = %q", got)
	}
	loaded, err := Load(path)
	if err != nil || loaded.RoutingStrategy != "fill-first" || loaded.WAABackend != WAABackendSpiderMonkey {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
}

// TestLoadInvalidValueUsesDefault 核对取值无效的项按默认值加载并记录键与原值
func TestLoadInvalidValueUsesDefault(t *testing.T) {
	path := writeTestEnv(t, "ROUTING_STRATEGY=fill-first\nWAA_BACKEND=unknown\nPER_ACCOUNT_CONCURRENCY=abc\nWARM_WORKER_LIMIT=20\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WAABackend != WAABackendSpiderMonkey || cfg.PerAccountConcurrency != defaultAccountConcurrency ||
		cfg.WarmWorkerLimit != defaultWarmWorkerLimit || cfg.MaxActiveWorkers != defaultMaxActiveWorkers || cfg.RoutingStrategy != "fill-first" {
		t.Fatalf("cfg = %+v", cfg)
	}
	var keys []string
	for _, item := range cfg.Invalid {
		keys = append(keys, item.Key+"="+item.Value)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"PER_ACCOUNT_CONCURRENCY=abc", "WAA_BACKEND=unknown", "WARM_WORKER_LIMIT=20"}) {
		t.Fatalf("invalid = %v", keys)
	}
}

// TestEnvironmentKeepsSavedFile 核对环境变量决定的项按环境变量生效，保存时保留 .env 中原有的值
func TestEnvironmentKeepsSavedFile(t *testing.T) {
	path := writeTestEnv(t, "ROUTING_STRATEGY=fill-first\n")
	t.Setenv("ROUTING_STRATEGY", "round-robin")
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RoutingStrategy != "round-robin" || !slices.Equal(saved.Environment, []string{"ROUTING_STRATEGY"}) {
		t.Fatalf("saved = %+v", saved)
	}
	update := Default()
	update.RoutingStrategy = "fill-first"
	update.RequestBodyLog = true
	update = update.KeepEnvironment(saved)
	if update.RoutingStrategy != "round-robin" {
		t.Fatalf("update = %+v", update)
	}
	if err := update.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readTestEnv(t, path); got != "ROUTING_STRATEGY=fill-first\nREQUEST_BODY_LOG=true\n" {
		t.Fatalf(".env = %q", got)
	}
}

func TestWebAPIKeyOverridesEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, key, afterRestart string
	}{
		{"unchanged", "env-key", "next-env-key"},
		{"replace", "web-key,second-key", "web-key,second-key"},
		{"clear", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeTestEnv(t, "PROXY_API_KEY=file-key\nROUTING_STRATEGY=fill-first\n")
			t.Setenv("PROXY_API_KEY", "env-key")
			t.Setenv("ROUTING_STRATEGY", "round-robin")
			saved, err := Load(path)
			if err != nil || saved.ProxyAPIKey != "env-key" || slices.Contains(saved.Environment, "PROXY_API_KEY") {
				t.Fatal("environment key did not remain an editable bootstrap value", err)
			}
			updated := saved
			updated.ProxyAPIKey, updated.RoutingStrategy = test.key, "fill-first"
			updated = updated.KeepEnvironment(saved)
			if updated.ProxyAPIKey != test.key || updated.RoutingStrategy != "round-robin" {
				t.Fatal("key edit was rejected or unrelated environment lock was removed")
			}
			if err := updated.Save(path); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PROXY_API_KEY", "next-env-key")
			loaded, err := Load(path)
			if err != nil || loaded.ProxyAPIKey != test.afterRestart {
				t.Fatal("saved web key was replaced by the process environment", err)
			}
			if err := loaded.Save(path); err != nil {
				t.Fatal(err)
			}
			reloaded, err := Load(path)
			if err != nil || reloaded.ProxyAPIKey != test.afterRestart {
				t.Fatal("a later settings save lost the web key override", err)
			}
		})
	}
}

// TestAPIKeyAccounts 核对绑定解析、已删除 key 的处理与无效绑定拒绝加载且错误不含 key。
func TestAPIKeyAccounts(t *testing.T) {
	accounts, err := ParseAPIKeyAccounts(" sk-a : a@example.com | b@example.com , sk:b:c@example.com", "sk-a, sk:b")
	if err != nil || len(accounts) != 2 || len(accounts["sk-a"]) != 2 || accounts["sk-a"][1] != "b@example.com" || accounts["sk:b"][0] != "c@example.com" {
		t.Fatalf("accounts=%v err=%v", accounts, err)
	}
	if accounts, err := ParseAPIKeyAccounts("sk-a:a@example.com,sk-gone:b@example.com", "sk-a"); err != nil || len(accounts) != 1 || accounts["sk-a"][0] != "a@example.com" {
		t.Fatalf("已删除 key 的绑定 accounts=%v err=%v", accounts, err)
	}
	for _, value := range []string{"sk-a", "sk-a:", "sk-a:a@example.com,sk-a:b@example.com"} {
		if _, err := ParseAPIKeyAccounts(value, "sk-a"); err == nil {
			t.Fatalf("%q accepted", value)
		}
	}
	cfg, err := Load(writeTestEnv(t, "PROXY_API_KEY=sk-secret\nAPI_KEY_ACCOUNTS=sk-secret:a@example.com,sk-gone:b@example.com\n"))
	if err != nil || cfg.APIKeyAccounts != "sk-secret:a@example.com,sk-gone:b@example.com" || len(cfg.Invalid) != 0 {
		t.Fatalf("含已删除 key 的绑定 value=%q invalid=%+v err=%v", cfg.APIKeyAccounts, cfg.Invalid, err)
	}
	cfg, err = Load(writeTestEnv(t, "PROXY_API_KEY=sk-secret\nAPI_KEY_ACCOUNTS=sk-secret:\n"))
	if err == nil || !strings.Contains(err.Error(), "API_KEY_ACCOUNTS") || strings.Contains(err.Error(), "sk-") {
		t.Fatalf("invalid binding must fail closed without exposing the key: %v", err)
	}
}

// TestEnvInvalidSecretOmitsValue 核对取值无效的凭据配置项只记录键名与原因，不记录原值
func TestEnvInvalidSecretOmitsValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PROXY=http://alice:s3cret@127.0.0.1:notaport\nWAA_BACKEND=unknown\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, invalid := range cfg.Invalid {
		values[invalid.Key] = invalid.Value
	}
	if value, ok := values["PROXY"]; !ok || value != "" {
		t.Fatalf("PROXY 无效记录=%q 存在=%v", value, ok)
	}
	if values["WAA_BACKEND"] != "unknown" {
		t.Fatalf("WAA_BACKEND 无效记录=%q", values["WAA_BACKEND"])
	}
}

// TestEnvKeepEnvironmentKeepsInvalid 核对保存设置时环境变量决定的无效项仍列在无效记录中
func TestEnvKeepEnvironmentKeepsInvalid(t *testing.T) {
	t.Setenv("WAA_BACKEND", "unknown")
	path := filepath.Join(t.TempDir(), ".env")
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := Default().KeepEnvironment(saved)
	if len(kept.Invalid) != 1 || kept.Invalid[0].Key != "WAA_BACKEND" || kept.WAABackend != WAABackendSpiderMonkey {
		t.Fatalf("保存后无效记录=%v 后端=%q", kept.Invalid, kept.WAABackend)
	}
}

func TestConfigKeyCoverage(t *testing.T) {
	defaults := Default().envValues()
	if len(configKeys) != len(configSetters) || len(configKeys) != len(defaults) {
		t.Fatalf("keys=%d setters=%d values=%d", len(configKeys), len(configSetters), len(defaults))
	}
	var parsed Config
	for index, key := range configKeys {
		setter, ok := configSetters[key]
		value, hasValue := defaults[key]
		if !ok || !hasValue || slices.Contains(configKeys[:index], key) {
			t.Fatalf("missing or duplicate config key %s", key)
		}
		if err := setter(&parsed, value); err != nil {
			t.Fatalf("default setter %s: %v", key, err)
		}
	}
	if !reflect.DeepEqual(parsed, Default()) {
		t.Fatalf("default round trip: %+v", parsed)
	}
	for _, check := range configChecks {
		for _, key := range check.keys {
			if !slices.Contains(configKeys[:], key) {
				t.Fatalf("check uses unknown key %s", key)
			}
		}
	}
}

func TestUpgradeEnvPreservesExplicitSettings(t *testing.T) {
	for _, channels := range []string{"playground,build", "build,playground", "playground", "build"} {
		t.Run(channels, func(t *testing.T) {
			want := Default()
			want.UpstreamChannels = parseUpstreamChannels(channels)
			want.FirstEventHedge = FirstEventHedgeAuto
			want.RequestTimeout = 5 * time.Minute
			want.AntiTruncation, want.AutoContinuation, want.ContinuationRewrite = true, false, false
			want.ProxyAPIKey, want.APIKeyAccounts = "test-key", "test-key:a@example.com"
			want.FirstEventHedgeModels = "custom-model"
			want.ModelMapping, want.ModelChannels = "alias:custom-model", "custom-model:build"
			path := writeTestEnv(t, "")
			legacy := want.envValues()
			legacy["WAA_BACKEND"] = "go"
			if err := writeEnv(path, legacy); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil || !reflect.DeepEqual(loaded, want) {
				t.Fatalf("legacy values changed: got %+v, want %+v, err=%v", loaded, want, err)
			}
			values, _, err := readEnvFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"UPSTREAM_CHANNELS", "FIRST_EVENT_HEDGE", "REQUEST_TIMEOUT", "ANTI_TRUNCATION", "AUTO_CONTINUATION", "CONTINUATION_REWRITE", "PROXY_API_KEY", "API_KEY_ACCOUNTS", "FIRST_EVENT_HEDGE_MODELS", "MODEL_MAPPING", "MODEL_CHANNELS"} {
				if values[key] != legacy[key] {
					t.Errorf("migration changed %s: %q != %q", key, values[key], legacy[key])
				}
			}
			before := readTestEnv(t, path)
			if _, err := Load(path); err != nil || readTestEnv(t, path) != before {
				t.Fatalf("migration is not idempotent: %v", err)
			}
			if err := loaded.Save(path); err != nil {
				t.Fatal(err)
			}
			loaded, err = Load(path)
			if err != nil || !reflect.DeepEqual(loaded, want) {
				t.Fatalf("differential save changed legacy settings: %+v, %v", loaded, err)
			}
		})
	}
}

func TestUpgradeEnvPreservesCustomCapacityAndCommentedFiles(t *testing.T) {
	want := Default()
	want.WarmWorkerLimit, want.MaxActiveWorkers, want.WarmStartupConcurrency, want.PerAccountConcurrency = 2, 4, 3, 2
	want.WAABackend = WAABackendCamoufox
	for _, commented := range []bool{false, true} {
		path := writeTestEnv(t, "")
		if err := writeEnv(path, want.envValues()); err != nil {
			t.Fatal(err)
		}
		before := readTestEnv(t, path)
		if commented {
			before = "# User-maintained configuration\n" + before
			if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := Load(path)
		if err != nil || !reflect.DeepEqual(loaded, want) {
			t.Fatalf("explicit capacity changed: %+v, %v", loaded, err)
		}
		if commented && readTestEnv(t, path) != before {
			t.Fatal("loading a commented file rewrote it")
		}
	}
}

func TestLocalAndNewKeysKeepEnvironmentAndSavedValues(t *testing.T) {
	file := map[string]string{
		"STRIP_THOUGHT_SIGNATURES": "true",
		"ANTI_TRUNCATION":          "true", "AUTO_CONTINUATION": "false", "CONTINUATION_REWRITE": "true",
		"WARM_WORKER_LIMIT": "2", "MAX_ACTIVE_WORKERS": "4", "WARM_STARTUP_CONCURRENCY": "3", "PER_ACCOUNT_CONCURRENCY": "2",
		"UPSTREAM_CHANNELS": "build", "WAA_BACKEND": WAABackendCamoufox, "FIRST_EVENT_HEDGE": FirstEventHedgeOff,
		"FIRST_EVENT_HEDGE_MODELS": "file-model", "API_KEY_ACCOUNTS": "file-key:a@example.com",
	}
	environment := map[string]string{
		"STRIP_THOUGHT_SIGNATURES": "false",
		"ANTI_TRUNCATION":          "false", "AUTO_CONTINUATION": "true", "CONTINUATION_REWRITE": "false",
		"WARM_WORKER_LIMIT": "0", "MAX_ACTIVE_WORKERS": "1", "WARM_STARTUP_CONCURRENCY": "1", "PER_ACCOUNT_CONCURRENCY": "1",
		"UPSTREAM_CHANNELS": "playground,build", "WAA_BACKEND": "go", "FIRST_EVENT_HEDGE": FirstEventHedgeAuto,
		"FIRST_EVENT_HEDGE_MODELS": "env-model", "API_KEY_ACCOUNTS": "env-key:b@example.com",
	}
	path := writeTestEnv(t, "")
	if err := writeEnv(path, file); err != nil {
		t.Fatal(err)
	}
	for key, value := range environment {
		t.Setenv(key, value)
	}
	saved, err := Load(path)
	if err != nil || len(saved.Invalid) != 0 {
		t.Fatalf("environment load: %+v, %v", saved, err)
	}
	updated := Default().KeepEnvironment(saved)
	if err := updated.Save(path); err != nil {
		t.Fatal(err)
	}
	for key, value := range environment {
		if key == "WAA_BACKEND" {
			value = WAABackendSpiderMonkey
		}
		if !slices.Contains(updated.Environment, key) || updated.envValues()[key] != value {
			t.Errorf("environment setting %s was not retained", key)
		}
	}
	values, _, err := readEnvFile(path)
	if err != nil || !reflect.DeepEqual(values, file) {
		t.Fatalf("saving locked keys changed file values: %v, %v", values, err)
	}
}

func TestLocalInvalidEnvironmentKeepsNoticeAndFile(t *testing.T) {
	content := "ANTI_TRUNCATION=true\nAUTO_CONTINUATION=false\nROUTING_STRATEGY=invalid\n"
	path := writeTestEnv(t, content)
	t.Setenv("ANTI_TRUNCATION", "invalid")
	t.Setenv("AUTO_CONTINUATION", "invalid")
	saved, err := Load(path)
	if err != nil || saved.AntiTruncation || !saved.AutoContinuation || len(saved.Invalid) != 3 {
		t.Fatalf("invalid local values did not use defaults: %+v, %v", saved, err)
	}
	update := Default()
	update.AntiTruncation, update.AutoContinuation = true, false
	update = update.KeepEnvironment(saved)
	if update.AntiTruncation || !update.AutoContinuation || len(update.Invalid) != 2 {
		t.Fatalf("environment defaults or invalid notices lost: %+v", update)
	}
	if err := update.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readTestEnv(t, path); got != "ANTI_TRUNCATION=true\nAUTO_CONTINUATION=false\n" {
		t.Fatalf("locked file values changed: %q", got)
	}
}

func TestAPIKeyAccountsInvalidEnvironmentAndSaveFailClosed(t *testing.T) {
	path := writeTestEnv(t, "PROXY_API_KEY=test-key\nAPI_KEY_ACCOUNTS=test-key:a@example.com\n")
	before := readTestEnv(t, path)
	cfg := Default()
	cfg.ProxyAPIKey = "test-key"
	for _, value := range []string{"test-key", "test-key:", "test-key:a@example.com,test-key:b@example.com"} {
		cfg.APIKeyAccounts = value
		if err := cfg.Save(path); err == nil || strings.Contains(err.Error(), "test-key") {
			t.Fatalf("invalid binding save error: %v", err)
		}
		if readTestEnv(t, path) != before {
			t.Fatal("invalid binding replaced the saved restrictions")
		}
		t.Setenv("API_KEY_ACCOUNTS", value)
		if _, err := Load(path); err == nil || strings.Contains(err.Error(), "test-key") {
			t.Fatalf("invalid environment binding must fail closed: %v", err)
		}
	}
}

func TestFirstEventHedgeModels(t *testing.T) {
	if got := ModelList(" models/gemini-a,gemini-a, , gemini-b "); !slices.Equal(got, []string{"gemini-a", "gemini-b"}) {
		t.Fatalf("model list = %v", got)
	}
	cfg, err := Load(writeTestEnv(t, "FIRST_EVENT_HEDGE=off\nFIRST_EVENT_HEDGE_MODELS=\n"))
	if err != nil || cfg.FirstEventHedge != FirstEventHedgeOff || cfg.FirstEventHedgeModels != "" {
		t.Fatalf("explicit hedge settings changed: %+v, %v", cfg, err)
	}
}
