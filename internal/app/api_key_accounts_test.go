package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestAPIKeyAccountsDropsRemovedKeyBinding(t *testing.T) {
	same := apiKeyAccounts("key-a,key-b", "key-a:a@example.com")
	if len(same) != 1 || !slices.Equal(same["key-a"], []string{"a@example.com"}) {
		t.Fatalf("key 列表不变时 %v", same)
	}
	rotated := apiKeyAccounts("key-new,key-b", "key-a:a@example.com,key-new:b@example.com")
	if _, kept := rotated["key-a"]; kept || len(rotated) != 1 || !slices.Equal(rotated["key-new"], []string{"b@example.com"}) {
		t.Fatalf("换 key 后 %v", rotated)
	}
}

func TestAPIKeyAccountsLiveSaveUpdatesAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.ProxyAPIKey, cfg.APIKeyAccounts = "key-a,key-b", "key-a:a@example.com"
	t.Setenv("PROXY_API_KEY", cfg.ProxyAPIKey)
	t.Setenv("REQUEST_TIMEOUT", "90s")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: cfg, current: configTestGeneration(t, t.Context(), path, cfg), logs: &logOutput{},
	}
	manager.settings.Store(requestSettingsOf(cfg))
	handler := api.NewHandler(nil, api.Config{APIKey: cfg.ProxyAPIKey, APIKeys: manager.apiKeys})
	check := func(key, origin string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("authentication status=%d want=%d", response.Code, want)
		}
	}
	check("key-a", "", http.StatusNotFound)
	check("key-new", "", http.StatusUnauthorized)
	value := runtimeConfigDTO(cfg)
	value.APIKeyAccounts = "key-a:b@example.com"
	value.RequestTimeout = "2m"
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.ServiceRestartRequired || updated.ManagementRestartRequired {
		t.Fatalf("binding update must be immediate: %+v, %v", updated, err)
	}
	if updated.RequestTimeout != "1m30s" || !slices.Contains(updated.Environment, "REQUEST_TIMEOUT") || slices.Contains(updated.Environment, "PROXY_API_KEY") {
		t.Fatal("only PROXY_API_KEY should be editable over an injected environment value")
	}
	service := manager.current.service.(*configTestService)
	if !slices.Equal(service.bindings["key-a"], []string{"b@example.com"}) {
		t.Fatalf("binding update not applied: %v", service.bindings)
	}
	value = updated
	value.APIKey, value.APIKeyAccounts = "key-new,key-b", "key-new:new@example.com"
	updated, err = manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.ManagementRestartRequired || updated.ServiceRestartRequired || updated.ActiveAPIKey != value.APIKey {
		t.Fatalf("authentication update did not apply immediately: %+v, %v", updated, err)
	}
	if len(service.bindings) != 1 || !slices.Equal(service.bindings["key-new"], []string{"new@example.com"}) {
		t.Fatalf("new key bindings did not apply: %v", service.bindings)
	}
	check("key-a", "", http.StatusUnauthorized)
	check("key-new", "", http.StatusNotFound)
	check("key-b", "", http.StatusNotFound)
	check("", "", http.StatusUnauthorized)
	before := service.bindings
	value.APIKey, value.APIKeyAccounts = "key-invalid", "key-invalid:"
	_, err = manager.UpdateRuntimeConfig(t.Context(), value)
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) || status.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("invalid binding error = %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.APIKeyAccounts != updated.APIKeyAccounts || !reflect.DeepEqual(service.bindings, before) {
		t.Fatalf("invalid save changed restrictions: %+v, %v", loaded, err)
	}
	check("key-new", "", http.StatusNotFound)
	check("key-invalid", "", http.StatusUnauthorized)
	value = updated
	value.APIKey, value.APIKeyAccounts = "", ""
	if updated, err = manager.UpdateRuntimeConfig(t.Context(), value); err != nil || updated.ManagementRestartRequired || updated.ActiveAPIKey != "" {
		t.Fatalf("clearing the key required a restart: %+v, %v", updated, err)
	}
	check("", "", http.StatusNotFound)
	check("", "https://example.com", http.StatusUnauthorized)
	t.Setenv("PROXY_API_KEY", "changed-after-save")
	if reloaded, err := config.Load(path); err != nil || reloaded.ProxyAPIKey != "" || reloaded.RequestTimeout != 90*time.Second {
		t.Fatal("clearing the injected key did not persist independently of other environment settings")
	}
}

func TestLiveConfigSaveSurvivesConcurrentStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.ProxyAPIKey, cfg.APIKeyAccounts = "key-a,key-b", "key-a:a@example.com,key-b:b@example.com"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		lifecycle: ctx, configPath: path, activeManagement: cfg, logs: &logOutput{},
		current: configTestGeneration(t, ctx, path, cfg),
	}
	manager.settings.Store(requestSettingsOf(cfg))
	next := configTestGeneration(t, ctx, path, cfg)
	building, release := make(chan struct{}), make(chan struct{})
	manager.factory = func(launch, _ context.Context, snapshot config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
		close(building)
		select {
		case <-release:
		case <-launch.Done():
			return nil, launch.Err()
		}
		next.config = snapshot
		return next, nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.startService(ctx)
		result <- err
	}()
	select {
	case <-building:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	value := runtimeConfigDTO(cfg)
	value.APIKey, value.APIKeyAccounts = "key-b,key-new", "key-b:changed@example.com,key-new:new@example.com"
	value.AntiTruncation, value.AutoContinuation = true, new(bool)
	value.WarmWorkerLimit, value.MaxActiveWorkers, value.WarmStartupConcurrency, value.PerAccountConcurrency = 1, 3, 2, 2
	value.RoutingStrategy, value.ModelChannels = "fill-first", "gemini-test:build"
	value.AccountOrder, value.ModelAccounts = "changed@example.com,new@example.com", "gemini-test:changed@example.com"
	value.RequestTimeout, value.ContinuationRewrite = "15m", false
	value.AuthStates = "next-auth"
	updated, err := manager.UpdateRuntimeConfig(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	service := next.service.(*configTestService)
	wantBindings := map[string][]string{"key-b": {"changed@example.com"}, "key-new": {"new@example.com"}}
	if manager.current != next || !reflect.DeepEqual(service.bindings, wantBindings) || service.capacity != [4]int{1, 3, 2, 2} ||
		!service.autoContinuationDisabled.Load() || !manager.antiTruncationEnabled() {
		t.Fatalf("startup replaced live settings: config=%+v bindings=%v capacity=%v", next.config, service.bindings, service.capacity)
	}
	if next.config.RoutingStrategy != value.RoutingStrategy || next.config.ModelChannels != value.ModelChannels ||
		next.config.AccountOrder != value.AccountOrder || next.config.ModelAccounts != value.ModelAccounts {
		t.Fatalf("startup replaced live routing: %+v", next.config)
	}
	if next.config.RequestTimeout != 15*time.Minute || next.config.ContinuationRewrite || next.config.AuthStates != cfg.AuthStates {
		t.Fatal("startup must apply newly saved request settings but retain the account directory it actually opened")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.APIKeyAccounts != updated.APIKeyAccounts || loaded.RequestTimeout != 15*time.Minute || loaded.ContinuationRewrite {
		t.Fatalf("startup changed saved configuration: %+v, %v", loaded, err)
	}
	active, err := manager.RuntimeConfig(ctx)
	if err != nil || active.ManagementRestartRequired || !active.ServiceRestartRequired || active.ActiveAPIKey != value.APIKey {
		t.Fatalf("pending changes lost their restart boundary: %+v, %v", active, err)
	}
	manager.factory = func(_ context.Context, lifecycle context.Context, cfg config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
		return configTestGeneration(t, lifecycle, path, cfg), nil
	}
	if status, err := manager.RestartService(ctx); err != nil || !status.Running {
		t.Fatalf("generation restart failed: %+v, %v", status, err)
	}
	active, err = manager.RuntimeConfig(ctx)
	if err != nil || active.ServiceRestartRequired || manager.current.config.AuthStates != value.AuthStates || active.ActiveAPIKey != value.APIKey {
		t.Fatal("generation restart did not apply the saved account directory and preserve the live key")
	}
}
