package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestAccountTaskLifecycleInterruptsLockedLogin(t *testing.T) {
	for _, operation := range []string{"start", "stop", "close"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cfg := config.Default()
			path := filepath.Join(t.TempDir(), ".env")
			if err := cfg.Save(path); err != nil {
				t.Fatal(err)
			}
			admin := newAccountTestAdmin(t)
			state, err := aistudio.ParseCookieImport("SAPISID=fixture; __Secure-1PAPISID=one; __Secure-3PAPISID=three")
			if err != nil {
				t.Fatal(err)
			}
			account, err := admin.addAccount(ctx, aistudio.DefaultAccountConfig("user@example.test"), state, "", "")
			if err != nil {
				t.Fatal(err)
			}
			entered, cleanup := make(chan struct{}), make(chan struct{})
			admin.login = accountLoginFunc(func(loginCtx context.Context, _ aistudio.IsolatedLoginRequest) (aistudio.IsolatedLoginResult, error) {
				close(entered)
				<-loginCtx.Done()
				<-cleanup
				return aistudio.IsolatedLoginResult{}, loginCtx.Err()
			})
			generation := &runtimeGeneration{
				service: admin.service, admin: configStartAdmin{admin}, config: cfg,
				lifecycleCancel: func() {}, closeRuntime: admin.workers.Close,
			}
			manager := &runtimeManager{
				lifecycle: ctx, current: generation, configPath: path, activeManagement: cfg,
				requests: newRequestRegistry(ctx), factory: func(_ context.Context, lifecycle context.Context, cfg config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
					return configTestGeneration(lifecycle, path, cfg), nil
				},
			}
			taskCtx := manager.AccountTaskContext()
			loginDone := make(chan error, 1)
			go func() {
				_, err := manager.LoginAccount(taskCtx, account.ID, nil)
				loginDone <- err
			}()
			<-entered
			transitionDone := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "start":
					_, err = manager.StartService(ctx)
				case "stop":
					_, err = manager.StopService(ctx)
				case "close":
					err = manager.Close()
				}
				transitionDone <- err
			}()
			select {
			case <-taskCtx.Done():
			case <-time.After(time.Second):
				t.Error("lifecycle transition waited for manager lock before canceling login")
				cancel()
			}
			if fresh := manager.AccountTaskContext(); fresh.Err() == nil {
				t.Error("new account task escaped cancellation during runtime transition")
			}
			select {
			case <-transitionDone:
				t.Error("runtime transition completed before login released the read lock")
			default:
			}
			close(cleanup)
			select {
			case err := <-loginDone:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("login error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled login retained manager read lock")
			}
			select {
			case err := <-transitionDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("runtime transition deadlocked after login cancellation")
			}
			if ctx.Err() == nil {
				fresh := manager.AccountTaskContext()
				if operation == "close" && fresh.Err() == nil || operation != "close" && fresh.Err() != nil {
					t.Fatal("task lifecycle was not renewed after stop/start or stayed open after close")
				}
			}
		})
	}
}

func TestAccountTaskCloseCancelsAndJoinsRuntimeReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := config.Default()
	path := filepath.Join(t.TempDir(), ".env")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	current := configTestGeneration(ctx, path, cfg)
	entered, canceled, release, nextClosed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	manager := &runtimeManager{
		lifecycle: ctx, configPath: path, activeManagement: cfg, current: current, requests: newRequestRegistry(ctx),
		factory: func(launch context.Context, lifecycle context.Context, cfg config.Config, _ *requestRegistry) (*runtimeGeneration, error) {
			close(entered)
			<-launch.Done()
			close(canceled)
			<-release
			next := configTestGeneration(lifecycle, path, cfg)
			next.closeRuntime = func() error { close(nextClosed); return nil }
			return next, nil
		},
	}
	started := make(chan error, 1)
	go func() {
		_, err := manager.StartService(ctx)
		started <- err
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Error("Close did not cancel the in-flight runtime factory")
		cancel()
	}
	select {
	case err := <-closed:
		t.Errorf("Close returned before replacement resources were released: %v", err)
		closed <- err
	default:
	}
	close(release)
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled runtime replacement did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join runtime replacement")
	}
	select {
	case <-nextClosed:
	default:
		t.Fatal("unpublished runtime was not closed")
	}
	if manager.current != current || manager.AccountTaskContext().Err() == nil {
		t.Fatal("Close allowed replacement or account tasks to revive")
	}
	if ctx.Err() == nil {
		if _, err := manager.StartService(ctx); err == nil {
			t.Fatal("closed manager accepted a new start")
		}
	}
}
