package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestAutoContinuationDisableSurvivesConcurrentStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	manager := &runtimeManager{
		lifecycle: ctx, configPath: path, activeManagement: cfg, logs: &logOutput{},
		current: configTestGeneration(ctx, path, cfg),
	}
	building := make(chan struct{})
	release := make(chan struct{})
	next := configTestGeneration(ctx, path, cfg)
	manager.factory = func(launch, _ context.Context, snapshot config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
		next.service.SetAutoContinuation(snapshot.AutoContinuation)
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
	// The next instance already captured "enabled" before the setting was saved.
	value := runtimeConfigDTO(cfg)
	value.AutoContinuation = new(bool)
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
	if manager.current != next || !next.service.(*configTestService).autoContinuationDisabled.Load() || manager.current.config.AutoContinuation {
		t.Fatal("publishing an older startup snapshot re-enabled automatic continuation")
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.AutoContinuation {
		t.Fatalf("startup overwrote the saved setting: %+v, %v", loaded, err)
	}
}
