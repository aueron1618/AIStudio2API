package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type configTestService struct {
	*trackedService
	capacity [4]int
	bindings map[string][]string
}

func (service *configTestService) ApplyCapacity(warm, active, startup, perAccount int) {
	service.capacity = [4]int{warm, active, startup, perAccount}
}

func (service *configTestService) SetAPIKeyAccounts(bindings map[string][]string) {
	service.bindings = bindings
	service.trackedService.SetAPIKeyAccounts(bindings)
}

type configStartAdmin struct {
	*runtimeAdmin
}

func (admin configStartAdmin) StartService(context.Context) (api.AdminStatus, error) {
	admin.service.state.Store(serviceRunning)
	return api.AdminStatus{State: "RUNNING", Running: true}, nil
}

func (admin configStartAdmin) StopService(context.Context) (api.AdminStatus, error) {
	admin.service.state.Store(serviceStopped)
	return api.AdminStatus{State: "STOPPED"}, nil
}

func newTestRequestRegistry(t *testing.T) *requestRegistry {
	t.Helper()
	registry := newRequestRegistry()
	t.Cleanup(registry.close)
	return registry
}

func configTestGeneration(t *testing.T, ctx context.Context, path string, cfg config.Config) *runtimeGeneration {
	t.Helper()
	pool := aistudio.NewAccountPool(nil, cfg.PerAccountConcurrency)
	requests := newTestRequestRegistry(t)
	service := &configTestService{trackedService: &trackedService{pool: pool, requests: requests}}
	service.SetAutoContinuation(cfg.AutoContinuation)
	mapping, _ := config.ParseModelMapping(cfg.ModelMapping)
	service.SetRequestPolicy(mapping, cfg.MaxAccountSwitches)
	admin := &runtimeAdmin{configPath: path, config: cfg, pool: pool, requests: requests, service: service.trackedService}
	admin.ApplyAccountSelection(cfg)
	return &runtimeGeneration{
		service: service, admin: configStartAdmin{admin}, config: cfg,
		lifecycleCancel: func() {}, closeRuntime: func() error { return nil },
	}
}

func TestInitialRuntimeReuseAndReplacement(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "reuse", true: "replace"}[changed], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			cfg := config.Default()
			if err := cfg.Save(path); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			initial := configTestGeneration(t, t.Context(), path, cfg)
			built, closed := 0, 0
			initial.closeRuntime = func() error { closed++; return nil }
			manager := &runtimeManager{
				lifecycle: t.Context(), configPath: path, current: initial, initial: initial,
				activeManagement: cfg, requests: initial.service.(*configTestService).requests,
				factory: func(_ context.Context, lifecycle context.Context, cfg config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
					built++
					return configTestGeneration(t, lifecycle, path, cfg), nil
				},
			}
			manager.settings.Store(requestSettingsOf(cfg))
			if changed {
				cfg.AutoContinuation = !cfg.AutoContinuation
				if err := cfg.Save(path); err != nil {
					t.Fatal(err)
				}
			}
			status, err := manager.StartService(t.Context())
			if err != nil || !status.Running || manager.initial != nil {
				t.Fatalf("initial start: %+v, %v", status, err)
			}
			if changed && (built != 1 || closed != 1 || manager.current == initial) || !changed && (built != 0 || closed != 0 || manager.current != initial) {
				t.Fatalf("initial generation was incorrectly reused or rebuilt: built=%d closed=%d", built, closed)
			}
			if _, err := manager.StopService(t.Context()); err != nil {
				t.Fatal(err)
			}
			previousBuilds := built
			if _, err := manager.StartService(t.Context()); err != nil || built != previousBuilds+1 {
				t.Fatalf("subsequent start reused the initial exception: built=%d err=%v", built, err)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestAdminConfig 验证凭据保存、密码保留、热加载键不触发重启与仅必要键触发重启标记
func TestAdminConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AdminAuthEnabled = true
	cfg.AdminUsername = "operator"
	cfg.AdminPassword = "test-password"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	generation := configTestGeneration(t, ctx, path, cfg)
	service := generation.service.(*configTestService)
	manager := &runtimeManager{activeManagement: cfg, current: generation, requests: service.requests, logs: &logOutput{}}
	value, err := manager.RuntimeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-password") || !value.AdminPasswordSet || !value.BuildNativeNonstream {
		t.Fatalf("config=%s", raw)
	}
	if value.AccountSwitchInterval != config.AccountSwitchIntervalOff || value.MaxQuotaSwitches != config.MaxQuotaSwitchesOff {
		t.Fatalf("default switch policy = interval:%q quota:%q", value.AccountSwitchInterval, value.MaxQuotaSwitches)
	}
	value.Proxy = "http://127.0.0.1:1234"
	value.AutoStart = true
	value.AccountSwitchInterval = "5"
	value.MaxQuotaSwitches = "1"
	if _, err := manager.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.AdminPassword != cfg.AdminPassword {
		t.Fatalf("password changed: %v", err)
	}
	if !saved.AutoStart || saved.AccountSwitchInterval != "5" || saved.MaxQuotaSwitches != "1" {
		t.Fatalf("saved fields mismatch: auto_start=%v interval=%q quota=%q", saved.AutoStart, saved.AccountSwitchInterval, saved.MaxQuotaSwitches)
	}
	service.state.Store(serviceRunning)
	manager.requests.start(aistudio.GenerateRequest{ID: "req-active", Model: "gemini-flash-latest"}, func() {})
	password := "new-test-password"
	value.AdminPassword = &password
	value.APIKey = "new-hot-api-key"
	value.BuildNativeNonstream = false
	value.YouTubeLinks = false
	value.StripThoughtSignatures = true
	value.ContinuationRewrite = false
	value.ContinuationPrompt = "custom prompt"
	value.RequestTimeout = "90s"
	value.InitTimeout = "45s"
	value.FirstEventTimeout = "30"
	value.FirstEventHedge = "10"
	value.FirstEventHedgeModels = "gemini-2.5-pro"
	value.UpstreamChannels = []string{"playground", "build"}
	value.DiscoverHiddenModels = true
	value, err = manager.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	if value.ManagementRestartRequired || value.ServiceRestartRequired || value.AdminPassword != nil || value.ActiveRequests != 1 {
		t.Fatalf("hot reload fields should not require restart and should report active_requests=1: %+v", value)
	}
	// 仅修改监听地址或预览监听地址才触发管理进程重启
	value.ListenAddr = "127.0.0.1:2999"
	decorated := manager.decorateRuntimeConfig(value, cfg)
	if !decorated.ManagementRestartRequired {
		t.Fatalf("listen_addr change should require management restart: %+v", decorated)
	}
	value.ListenAddr = cfg.ListenAddr
	value.PreviewListenAddr = "127.0.0.1:3050"
	decorated = manager.decorateRuntimeConfig(value, cfg)
	if !decorated.ManagementRestartRequired {
		t.Fatalf("preview_listen_addr change should require management restart: %+v", decorated)
	}
	value.PreviewListenAddr = cfg.PreviewListenAddr
	// 修改 WAA_BACKEND / AISTUDIO_AUTH_STATES 仅在生成服务运行时触发重启标记，已停止时不触发
	value.WAABackend = config.WAABackendCamoufox
	service.state.Store(serviceStopped)
	if got := manager.decorateRuntimeConfig(value, cfg); got.ServiceRestartRequired {
		t.Fatalf("stopped service should not require restart: %+v", got)
	}
	service.state.Store(serviceRunning)
	if got := manager.decorateRuntimeConfig(value, cfg); !got.ServiceRestartRequired {
		t.Fatalf("running service should require restart on WAA_BACKEND change: %+v", got)
	}
	value.WAABackend = cfg.WAABackend
	value.AuthStates = "auth-other"
	if got := manager.decorateRuntimeConfig(value, cfg); !got.ServiceRestartRequired {
		t.Fatalf("running service should require restart on AISTUDIO_AUTH_STATES change: %+v", got)
	}
	value.AuthStates = cfg.AuthStates
	// 非法换号间隔与额度换号上限返回 400
	badInterval := value
	badInterval.AccountSwitchInterval = "0"
	if _, err := manager.UpdateRuntimeConfig(ctx, badInterval); err == nil {
		t.Fatal("ACCOUNT_SWITCH_INTERVAL=0 should be rejected")
	}
	badQuota := value
	badQuota.MaxQuotaSwitches = "-1"
	if _, err := manager.UpdateRuntimeConfig(ctx, badQuota); err == nil {
		t.Fatal("MAX_QUOTA_SWITCHES=-1 should be rejected")
	}
	value.AdminPassword = new(string)
	_, err = manager.UpdateRuntimeConfig(ctx, value)
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) || status.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("empty enabled password error = %v", err)
	}
	value.AdminAuthEnabled = false
	if _, err := manager.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
}

func TestAutoContinuationConfigIsImmediateAndPreservesOmittedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	generation := configTestGeneration(t, t.Context(), path, cfg)
	service := generation.service.(*configTestService)
	manager := &runtimeManager{activeManagement: cfg, current: generation, logs: &logOutput{}}
	value, err := manager.RuntimeConfig(t.Context())
	if err != nil || value.AutoContinuation == nil || !*value.AutoContinuation {
		t.Fatalf("missing enabled default in management DTO: %+v, %v", value, err)
	}
	disabled := false
	value.AutoContinuation = &disabled
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.AutoContinuation == nil || *updated.AutoContinuation || !service.autoContinuationDisabled.Load() {
		t.Fatalf("disable did not apply immediately: %+v, %v", updated, err)
	}
	if !sameDataConfig(updated, cfg, dataConfigOverrides{}) {
		t.Fatal("auto continuation incorrectly requires a service restart")
	}
	updated.AutoContinuation = nil
	if _, err := manager.UpdateRuntimeConfig(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.AutoContinuation || !service.autoContinuationDisabled.Load() {
		t.Fatalf("omitted field re-enabled continuation: %+v, %v", loaded, err)
	}
	enabled := true
	updated.AutoContinuation = &enabled
	if _, err := manager.UpdateRuntimeConfig(t.Context(), updated); err != nil || service.autoContinuationDisabled.Load() {
		t.Fatalf("re-enabling failed: %v", err)
	}
}

func TestLocalRuntimeConfigEnvironmentLocksAndDTO(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AntiTruncation, cfg.AutoContinuation = true, false
	cfg.ProxyAPIKey, cfg.APIKeyAccounts = "test-key", "test-key:file@example.com"
	cfg.AccountOrder, cfg.ModelAccounts = "file@example.com", "test-model:file@example.com"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTI_TRUNCATION", "false")
	t.Setenv("AUTO_CONTINUATION", "true")
	t.Setenv("API_KEY_ACCOUNTS", "test-key:env@example.com")
	t.Setenv("ACCOUNT_ORDER", "env@example.com")
	t.Setenv("MODEL_ACCOUNTS", "test-model:env@example.com")
	t.Setenv("FIRST_EVENT_HEDGE_MODELS", "locked-model")
	t.Setenv("WARM_WORKER_LIMIT", "0")
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: saved, current: configTestGeneration(t, t.Context(), path, saved), logs: &logOutput{},
	}
	manager.settings.Store(requestSettingsOf(saved))
	value, err := manager.RuntimeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"anti_truncation", "auto_continuation", "first_event_hedge_models", "api_key_accounts", "account_order", "model_accounts", "environment", "invalid"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("management DTO is missing %s", key)
		}
	}
	value.AntiTruncation, value.AutoContinuation = true, new(bool)
	value.APIKeyAccounts, value.FirstEventHedgeModels = "test-key:changed@example.com", "changed-model"
	value.AccountOrder, value.ModelAccounts = "changed@example.com", "test-model:changed@example.com"
	value.WarmWorkerLimit = 1
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.AntiTruncation || updated.AutoContinuation == nil || !*updated.AutoContinuation ||
		updated.APIKeyAccounts != saved.APIKeyAccounts || updated.FirstEventHedgeModels != saved.FirstEventHedgeModels || updated.WarmWorkerLimit != 0 {
		t.Fatalf("environment overrides were changed: %+v, %v", updated, err)
	}
	service := manager.current.service.(*configTestService)
	if service.autoContinuationDisabled.Load() || manager.antiTruncationEnabled() || !slices.Equal(service.bindings["test-key"], []string{"env@example.com"}) {
		t.Fatal("locked runtime values were changed")
	}
	if updated.AccountOrder != saved.AccountOrder || updated.ModelAccounts != saved.ModelAccounts ||
		manager.current.config.AccountOrder != saved.AccountOrder || manager.current.config.ModelAccounts != saved.ModelAccounts {
		t.Fatal("locked account order or model bindings were changed")
	}
	if updated.ServiceRestartRequired || updated.ManagementRestartRequired {
		t.Fatalf("locked values incorrectly require a restart: %+v", updated)
	}
}

func TestContinuationAndHedgeControlsDoNotRequireRestart(t *testing.T) {
	cfg := config.Default()
	value := runtimeConfigDTO(cfg)
	value.AutoContinuation, value.AntiTruncation = new(bool), true
	value.ContinuationRewrite = !cfg.ContinuationRewrite
	value.FirstEventHedgeModels = "different-model"
	if !sameDataConfig(value, cfg, dataConfigOverrides{}) {
		t.Fatal("live continuation or hedge controls required a restart")
	}
}

func TestRequestPolicySaveAppliesWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	generation := configTestGeneration(t, t.Context(), path, cfg)
	manager := &runtimeManager{activeManagement: cfg, current: generation, logs: &logOutput{}}
	previous := requestSettingsOf(cfg)
	manager.settings.Store(previous)
	value := runtimeConfigDTO(cfg)
	value.ModelMapping, value.MaxAccountSwitches = "friendly:shared", 2
	value.NonstreamKeepalive, value.FakeStream = !cfg.NonstreamKeepalive, !cfg.FakeStream
	value.SearchModelAliases, value.GoogleSearch = !cfg.SearchModelAliases, !cfg.GoogleSearch
	value.ThinkingGuard, value.UserMessageRewrite = true, "traditional"
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.ServiceRestartRequired || updated.ManagementRestartRequired || manager.current != generation {
		t.Fatalf("request policy required a restart: %+v, %v", updated, err)
	}
	service := generation.service.(*configTestService)
	if service.mappedModel("friendly") != "shared" || service.maxAccountSwitches.Load() != 2 {
		t.Fatal("saved request policy did not reach the active service")
	}
	settings := manager.settings.Load()
	if settings == previous || *previous != *requestSettingsOf(cfg) || *settings != *requestSettingsOf(generation.config) {
		t.Fatal("saving request settings mutated the old snapshot or failed to publish the new one")
	}
	if settings.modes.NonstreamKeepalive != value.NonstreamKeepalive || settings.modes.FakeStream != value.FakeStream ||
		settings.searchModelAliases != value.SearchModelAliases || settings.googleSearch != value.GoogleSearch ||
		!settings.thinkingGuard || settings.userMessageRewrite != "traditional" {
		t.Fatal("new requests did not receive the saved response, search, guard and rewrite settings")
	}
}

func TestRuntimeHotProxyAndTimeoutRespectLaunchOverride(t *testing.T) {
	const initial = "http://127.0.0.1:9100"
	const edited = "http://127.0.0.1:9101"
	for name, override := range map[string]string{"saved": "", "cli": "http://127.0.0.1:9102"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".env")
			cfg := config.Default()
			cfg.AutoStart, cfg.Proxy, cfg.AuthStates = false, initial, filepath.Join(dir, "accounts")
			if err := cfg.Save(path); err != nil {
				t.Fatal(err)
			}
			overrides := dataConfigOverrides{}
			want := edited
			if override != "" {
				overrides.proxy = &override
				overrides.Apply(&cfg)
				want = override
			}
			manager, err := newRuntimeManager(t.Context(), path, cfg, overrides)
			if err != nil {
				t.Fatal(err)
			}
			manager.logs = &logOutput{}
			t.Cleanup(func() {
				if err := manager.Close(); err != nil {
					t.Error(err)
				}
				manager.requests.close()
			})
			value, err := manager.RuntimeConfig(t.Context())
			if err != nil || value.Proxy != initial {
				t.Fatalf("saved proxy=%q, err=%v", value.Proxy, err)
			}
			value.Proxy, value.InitTimeout, value.RequestTimeout = edited, "45s", "90s"
			value, err = manager.UpdateRuntimeConfig(t.Context(), value)
			if err != nil || value.Proxy != edited || value.ActiveProxy != want || len(value.ProxyPool) != 1 || value.ProxyPool[0].Proxy != want || manager.egressProxy() != want {
				t.Fatalf("saved/active proxy mismatch: saved=%q active=%q pool=%v err=%v", value.Proxy, value.ActiveProxy, value.ProxyPool, err)
			}
			service := manager.current.service.(*trackedService)
			if service.workers.currentInitTimeout() != 45*time.Second || service.requestTimeout() != 90*time.Second {
				t.Fatal("saved deadlines did not reach the active worker and request managers")
			}
		})
	}
}

func TestAccountTimezoneNormalization(t *testing.T) {
	for _, test := range []struct {
		input, want string
		invalid     bool
	}{
		{"", "", false},
		{" \ufeff\u200b ", "", false},
		{" \ufeffAsia/\u200bShanghai\u2066 ", "Asia/Shanghai", false},
		{"Etc/UTC", "Etc/UTC", false},
		{"Mars/Olympus", "", true},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := accountTimezone(test.input)
			if got != test.want || (err != nil) != test.invalid {
				t.Fatalf("accountTimezone(%q) = %q, %v", test.input, got, err)
			}
		})
	}
}

func TestStripThoughtSignaturesAppliesOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: cfg, current: configTestGeneration(t, t.Context(), path, cfg), logs: &logOutput{},
	}
	manager.settings.Store(requestSettingsOf(cfg))
	value, err := manager.RuntimeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil || !strings.Contains(string(raw), `"strip_thought_signatures":false`) || strings.Contains(string(raw), "temporary_chat") {
		t.Fatal("management DTO omitted the new field or retained TemporaryChat")
	}
	value.StripThoughtSignatures = true
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || !updated.StripThoughtSignatures || updated.ServiceRestartRequired || !manager.current.config.StripThoughtSignatures {
		t.Fatal("signature setting was not applied immediately")
	}
	saved, err := config.Load(path)
	if err != nil || !saved.StripThoughtSignatures {
		t.Fatal("signature setting was not persisted")
	}
	t.Setenv("STRIP_THOUGHT_SIGNATURES", "false")
	updated, err = manager.UpdateRuntimeConfig(t.Context(), updated)
	if err != nil || updated.StripThoughtSignatures || !slices.Contains(updated.Environment, "STRIP_THOUGHT_SIGNATURES") {
		t.Fatal("signature setting ignored its environment lock")
	}
}
