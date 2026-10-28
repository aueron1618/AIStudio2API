package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func memoryTestManager(t *testing.T, warm, active int) *accountWorkerManager {
	t.Helper()
	account := &aistudio.Account{ID: "memory@example.test", Config: aistudio.DefaultAccountConfig("memory@example.test")}
	pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 1)
	manager := newAccountWorkerManager(pool, []*aistudio.Account{account}, newRequestRegistry(t.Context()), "", "", time.Second, warm, active, 1, false)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

func TestZeroWarmPoolStartsWithoutWorker(t *testing.T) {
	manager := memoryTestManager(t, 0, 1)
	for range 2 {
		if err := <-manager.StartPrewarm(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if manager.PrewarmTarget() != 0 || len(manager.WarmAccountIDs()) != 0 || len(manager.openings) != 0 {
		t.Fatal("zero warm pool started a Worker")
	}
	if manager.idleTimeout() != 30*time.Second {
		t.Fatal("zero warm pool must reclaim quickly")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := <-manager.StartPrewarm(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prewarm: %v", err)
	}
}

func TestGoWorkerStartupConcurrencyIsBounded(t *testing.T) {
	manager := memoryTestManager(t, 1, 5)
	if manager.startup.size() != 1 {
		t.Fatal("pure Go ignored startup concurrency")
	}
	if manager.idleTimeout() != 5*time.Minute {
		t.Fatal("explicit warm pools must keep existing timeout")
	}
	manager.SetCapacity(0, 3, 2)
	if manager.warmLimit() != 0 || manager.activeLimit() != 3 || manager.startup.size() != 2 || manager.idleTimeout() != 30*time.Second {
		t.Fatal("live capacity update lost zero-warm or Go startup limits")
	}
}

func TestZeroWarmPoolEvictionReleasesRuntimeLease(t *testing.T) {
	manager := memoryTestManager(t, 0, 1)
	account := manager.accounts["memory@example.test"]
	// A held runtime lease is enough to exercise the real reset/release path without a browser.
	account.runtimeLease = &aistudio.AccountRuntimeLease{}
	account.warm.Store(true)
	account.readyAt.Store(time.Now().Add(-time.Minute).UnixNano())
	if released, err := manager.evictIdleWorker(t.Context(), account.id); err != nil || !released {
		t.Fatalf("idle runtime lease not released: %v, %v", released, err)
	}
	if account.warm.Load() || account.runtimeLease != nil {
		t.Fatal("runtime slot not released")
	}
	if manager.reapIdleWorker(time.Now()) {
		t.Fatal("empty pool reported another release")
	}
}

func TestZeroWarmPoolKeepsBusyAndRecentWorkers(t *testing.T) {
	manager := memoryTestManager(t, 0, 1)
	account := manager.accounts["memory@example.test"]
	account.warm.Store(true)
	account.readyAt.Store(time.Now().UnixNano())
	if manager.reapIdleWorker(time.Now().Add(-30 * time.Second)) {
		t.Fatal("recent Worker was reaped")
	}
	account.readyAt.Store(time.Now().Add(-time.Minute).UnixNano())
	lease, err := manager.pool.AcquireAccount(t.Context(), account.id)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if manager.reapIdleWorker(time.Now()) {
		t.Fatal("busy Worker was reaped")
	}
}
