package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestAntiTruncationRuntimeConfig(t *testing.T) {
	cfg := config.Default()
	value := runtimeConfigDTO(cfg)
	if value.AntiTruncation {
		t.Fatal("anti-truncation must be opt-in")
	}
	value.AntiTruncation = true
	if !sameDataConfig(value, cfg, dataConfigOverrides{}) {
		t.Fatal("anti-truncation must not require restarting the generation service")
	}
	manager := &runtimeManager{current: &runtimeGeneration{config: cfg}}
	if manager.antiTruncationEnabled() {
		t.Fatal("disabled setting was not reflected")
	}
	manager.current.config.AntiTruncation = true
	if !manager.antiTruncationEnabled() || !runtimeConfigDTO(manager.current.config).AntiTruncation {
		t.Fatal("enabled setting was not reflected")
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	pool := aistudio.NewAccountPool(nil, 1)
	admin := &runtimeAdmin{configPath: path, requests: newRequestRegistry(t.Context()), pool: pool, service: &trackedService{pool: pool}}
	for _, enabled := range []bool{true, false} {
		value.AntiTruncation = enabled
		updated, err := admin.UpdateRuntimeConfig(t.Context(), value)
		if err != nil || updated.AntiTruncation != enabled {
			t.Fatalf("admin setting enabled=%t: %+v, %v", enabled, updated, err)
		}
		saved, err := config.Load(path)
		if err != nil || saved.AntiTruncation != enabled {
			t.Fatalf("saved setting enabled=%t: %+v, %v", enabled, saved, err)
		}
	}
}

func TestAntiTruncationDisableSurvivesConcurrentStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	cfg.AntiTruncation = true
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		lifecycle: ctx, configPath: path, activeManagement: cfg, logs: &logOutput{},
		current: configTestGeneration(ctx, path, cfg),
	}
	building, release := make(chan struct{}), make(chan struct{})
	next := configTestGeneration(ctx, path, cfg)
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
	value.AntiTruncation = false
	if _, err := manager.UpdateRuntimeConfig(ctx, value); err != nil {
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
	if manager.current != next || manager.antiTruncationEnabled() {
		t.Fatal("publishing an older startup snapshot re-enabled anti-truncation")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.AntiTruncation {
		t.Fatalf("startup overwrote the saved setting: %+v, %v", loaded, err)
	}
}
