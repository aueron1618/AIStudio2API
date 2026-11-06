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

func configTestGeneration(ctx context.Context, path string, cfg config.Config) *runtimeGeneration {
	pool := aistudio.NewAccountPool(nil, cfg.PerAccountConcurrency)
	requests := newRequestRegistry(ctx)
	service := &configTestService{trackedService: &trackedService{pool: pool, requests: requests}}
	service.SetAutoContinuation(cfg.AutoContinuation)
	admin := &runtimeAdmin{configPath: path, config: cfg, pool: pool, requests: requests, service: service.trackedService}
	admin.applyRoutingConfig(cfg.RoutingStrategy, cfg.ModelChannels)
	return &runtimeGeneration{
		service: service, admin: configStartAdmin{admin}, config: cfg,
		lifecycleCancel: func() {}, closeRuntime: func() error { return nil },
	}
}

// TestAdminConfig 验证凭据保存、密码保留与管理重启标记
func TestAdminConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AdminAuthEnabled = true
	cfg.AdminUsername = "operator"
	cfg.AdminPassword = "test-password"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := aistudio.NewAccountPool(nil, 1)
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(ctx), pool: pool, service: &trackedService{pool: pool}}
	value, err := admin.RuntimeConfig(ctx)
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
	value.Proxy = "http://127.0.0.1:1234"
	value.AutoStart = true
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.AdminPassword != cfg.AdminPassword {
		t.Fatalf("password changed: %v", err)
	}
	if !saved.AutoStart {
		t.Fatal("auto_start was not saved")
	}
	manager := &runtimeManager{activeManagement: cfg}
	password := "new-test-password"
	value.AdminPassword = &password
	value, err = admin.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	decorated := manager.decorateRuntimeConfig(value, cfg)
	if !decorated.ManagementRestartRequired || decorated.AdminPassword != nil {
		t.Fatalf("restart=%+v", decorated)
	}
	value.AdminPassword = new(string)
	_, err = admin.UpdateRuntimeConfig(ctx, value)
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) || status.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("empty enabled password error = %v", err)
	}
	value.AdminAuthEnabled = false
	if _, err := admin.UpdateRuntimeConfig(ctx, value); err != nil {
		t.Fatal(err)
	}
}

func TestAutoContinuationConfigIsImmediateAndPreservesOmittedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	pool := aistudio.NewAccountPool(nil, 1)
	service := &trackedService{pool: pool}
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(t.Context()), pool: pool, service: service}
	value, err := admin.RuntimeConfig(t.Context())
	if err != nil || value.AutoContinuation == nil || !*value.AutoContinuation {
		t.Fatalf("missing enabled default in management DTO: %+v, %v", value, err)
	}
	disabled := false
	value.AutoContinuation = &disabled
	updated, err := admin.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.AutoContinuation == nil || *updated.AutoContinuation || !service.autoContinuationDisabled.Load() {
		t.Fatalf("disable did not apply immediately: %+v, %v", updated, err)
	}
	if !sameDataConfig(updated, cfg, dataConfigOverrides{}) {
		t.Fatal("auto continuation incorrectly requires a service restart")
	}
	updated.AutoContinuation = nil
	if _, err := admin.UpdateRuntimeConfig(t.Context(), updated); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.AutoContinuation || !service.autoContinuationDisabled.Load() {
		t.Fatalf("omitted field re-enabled continuation: %+v, %v", loaded, err)
	}
	enabled := true
	updated.AutoContinuation = &enabled
	if _, err := admin.UpdateRuntimeConfig(t.Context(), updated); err != nil || service.autoContinuationDisabled.Load() {
		t.Fatalf("re-enabling failed: %v", err)
	}
}

func TestLocalRuntimeConfigEnvironmentLocksAndDTO(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AntiTruncation, cfg.AutoContinuation = true, false
	cfg.ProxyAPIKey, cfg.APIKeyAccounts = "test-key", "test-key:file@example.com"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTI_TRUNCATION", "false")
	t.Setenv("AUTO_CONTINUATION", "true")
	t.Setenv("API_KEY_ACCOUNTS", "test-key:env@example.com")
	t.Setenv("FIRST_EVENT_HEDGE_MODELS", "locked-model")
	t.Setenv("WARM_WORKER_LIMIT", "0")
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: saved, current: configTestGeneration(t.Context(), path, saved), logs: &logOutput{},
	}
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
	for _, key := range []string{"anti_truncation", "auto_continuation", "first_event_hedge_models", "api_key_accounts", "environment", "invalid"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("management DTO is missing %s", key)
		}
	}
	value.AntiTruncation, value.AutoContinuation = true, new(bool)
	value.APIKeyAccounts, value.FirstEventHedgeModels = "test-key:changed@example.com", "changed-model"
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
	if updated.ServiceRestartRequired || updated.ManagementRestartRequired {
		t.Fatalf("locked values incorrectly require a restart: %+v", updated)
	}
}

func TestContinuationControlsHaveIndependentRestartBoundaries(t *testing.T) {
	cfg := config.Default()
	value := runtimeConfigDTO(cfg)
	value.AutoContinuation, value.AntiTruncation = new(bool), true
	if !sameDataConfig(value, cfg, dataConfigOverrides{}) {
		t.Fatal("live controls require a restart")
	}
	value.ContinuationRewrite = !cfg.ContinuationRewrite
	if sameDataConfig(value, cfg, dataConfigOverrides{}) {
		t.Fatal("input rewrite lost its independent restart boundary")
	}
	value = runtimeConfigDTO(cfg)
	value.FirstEventHedgeModels = "different-model"
	if sameDataConfig(value, cfg, dataConfigOverrides{}) {
		t.Fatal("hedge model changes must be reflected in the restart marker")
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

func TestStripThoughtSignaturesConfigAndRestartBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: cfg, current: configTestGeneration(t.Context(), path, cfg), logs: &logOutput{},
	}
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
	if err != nil || !updated.StripThoughtSignatures || !updated.ServiceRestartRequired || manager.current.config.StripThoughtSignatures {
		t.Fatal("signature setting lost its save or restart boundary")
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
