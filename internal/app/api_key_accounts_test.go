package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

// TestAPIKeyAccountsKeepRemovedKeyBinding 核对绑定按保存的 key 列表立即生效，已从列表移除而管理服务仍接受的 key 沿用管理服务启动时的绑定
func TestAPIKeyAccountsKeepRemovedKeyBinding(t *testing.T) {
	active := config.Config{ProxyAPIKey: "key-a,key-b", APIKeyAccounts: "key-a:b@example.com"}
	same := apiKeyAccounts("key-a,key-b", "key-a:a@example.com", active)
	if len(same) != 1 || !slices.Equal(same["key-a"], []string{"a@example.com"}) {
		t.Fatalf("key 列表不变时 %v", same)
	}
	rotated := apiKeyAccounts("key-new,key-b", "key-new:b@example.com", active)
	if len(rotated) != 2 || !slices.Equal(rotated["key-a"], []string{"b@example.com"}) || !slices.Equal(rotated["key-new"], []string{"b@example.com"}) {
		t.Fatalf("换 key 后 %v", rotated)
	}
}

func TestAPIKeyAccountsLiveSaveKeepsAuthenticationRestartBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.ProxyAPIKey, cfg.APIKeyAccounts = "key-a,key-b", "key-a:a@example.com"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		activeManagement: cfg, current: configTestGeneration(t.Context(), path, cfg), logs: &logOutput{},
	}
	value := runtimeConfigDTO(cfg)
	value.APIKeyAccounts = "key-a:b@example.com"
	updated, err := manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || updated.ServiceRestartRequired || updated.ManagementRestartRequired {
		t.Fatalf("binding update must be immediate: %+v, %v", updated, err)
	}
	service := manager.current.service.(*configTestService)
	if !slices.Equal(service.bindings["key-a"], []string{"b@example.com"}) {
		t.Fatalf("binding update not applied: %v", service.bindings)
	}
	value = updated
	value.APIKey, value.APIKeyAccounts = "key-new,key-b", "key-new:new@example.com"
	updated, err = manager.UpdateRuntimeConfig(t.Context(), value)
	if err != nil || !updated.ManagementRestartRequired || updated.ServiceRestartRequired || updated.ActiveAPIKey != cfg.ProxyAPIKey {
		t.Fatalf("authentication restart boundary changed: %+v, %v", updated, err)
	}
	if !slices.Equal(service.bindings["key-a"], []string{"a@example.com"}) || !slices.Equal(service.bindings["key-new"], []string{"new@example.com"}) {
		t.Fatalf("removed active key did not retain its startup restriction: %v", service.bindings)
	}
	before := service.bindings
	value.APIKeyAccounts = "key-new:"
	_, err = manager.UpdateRuntimeConfig(t.Context(), value)
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) || status.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("invalid binding error = %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.APIKeyAccounts != updated.APIKeyAccounts || !reflect.DeepEqual(service.bindings, before) {
		t.Fatalf("invalid save changed restrictions: %+v, %v", loaded, err)
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
		current: configTestGeneration(ctx, path, cfg),
	}
	next := configTestGeneration(ctx, path, cfg)
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
	value.RequestTimeout, value.ContinuationRewrite = "15m", false
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
	wantBindings := map[string][]string{"key-a": {"a@example.com"}, "key-b": {"changed@example.com"}, "key-new": {"new@example.com"}}
	if manager.current != next || !reflect.DeepEqual(service.bindings, wantBindings) || service.capacity != [4]int{1, 3, 2, 2} ||
		!service.autoContinuationDisabled.Load() || !manager.antiTruncationEnabled() {
		t.Fatalf("startup replaced live settings: config=%+v bindings=%v capacity=%v", next.config, service.bindings, service.capacity)
	}
	if next.config.RoutingStrategy != value.RoutingStrategy || next.config.ModelChannels != value.ModelChannels {
		t.Fatalf("startup replaced live routing: %+v", next.config)
	}
	if next.config.RequestTimeout != cfg.RequestTimeout || next.config.ContinuationRewrite != cfg.ContinuationRewrite {
		t.Fatal("restart-bound settings were reported as applied to an older runtime")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.APIKeyAccounts != updated.APIKeyAccounts || loaded.RequestTimeout != 15*time.Minute || loaded.ContinuationRewrite {
		t.Fatalf("startup changed saved configuration: %+v, %v", loaded, err)
	}
	active, err := manager.RuntimeConfig(ctx)
	if err != nil || !active.ManagementRestartRequired || !active.ServiceRestartRequired || active.ActiveAPIKey != cfg.ProxyAPIKey {
		t.Fatalf("pending changes lost their restart boundary: %+v, %v", active, err)
	}
}
