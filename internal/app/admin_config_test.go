package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

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
