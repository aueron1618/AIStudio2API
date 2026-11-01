package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type continuationStartAdmin struct {
	generationAdmin
	service *trackedService
}

func (admin continuationStartAdmin) StartService(context.Context) (api.AdminStatus, error) {
	admin.service.state.Store(serviceRunning)
	return api.AdminStatus{State: "RUNNING", Running: true}, nil
}

func (admin continuationStartAdmin) StopService(context.Context) (api.AdminStatus, error) {
	admin.service.state.Store(serviceStopped)
	return api.AdminStatus{State: "STOPPED"}, nil
}

func TestAutoContinuationDisableSurvivesConcurrentStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".env")
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	current := &trackedService{}
	current.SetAutoContinuation(true)
	manager := &runtimeManager{
		lifecycle: ctx, configPath: path,
		current: &runtimeGeneration{
			service: current, admin: continuationStartAdmin{service: current}, config: cfg,
			lifecycleCancel: func() {}, closeRuntime: func() error { return nil },
		},
	}
	building := make(chan struct{})
	release := make(chan struct{})
	next := &trackedService{}
	manager.factory = func(launch, _ context.Context, snapshot config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
		next.SetAutoContinuation(snapshot.AutoContinuation)
		close(building)
		select {
		case <-release:
		case <-launch.Done():
			return nil, launch.Err()
		}
		return &runtimeGeneration{
			service: next, admin: continuationStartAdmin{service: next}, config: snapshot,
			lifecycleCancel: func() {}, closeRuntime: func() error { return nil },
		}, nil
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
	manager.mu.Lock()
	manager.current.service.SetAutoContinuation(false)
	manager.current.config.AutoContinuation = false
	manager.continuationRevision++
	manager.mu.Unlock()
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if manager.current.service != next || !next.autoContinuationDisabled.Load() || manager.current.config.AutoContinuation {
		t.Fatal("publishing an older startup snapshot re-enabled automatic continuation")
	}
}
