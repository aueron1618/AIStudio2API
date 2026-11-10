package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

type workerBudgetProbe struct {
	mu            sync.Mutex
	live          int
	peak          int
	starting      int
	startupPeak   int
	events        []string
	init          func(context.Context, string) error
	reload        func(context.Context, string) error
	headers       func(context.Context, string) error
	beforeClose   func(string)
	closeFailures int
}

type budgetWorker struct {
	probe    *workerBudgetProbe
	id       string
	phase    aistudio.WorkerPhase
	revision int
}

func (probe *workerBudgetProbe) begin(kind, id string) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.live++
	probe.starting++
	probe.peak = max(probe.peak, probe.live)
	probe.startupPeak = max(probe.startupPeak, probe.starting)
	probe.events = append(probe.events, kind+":"+id)
}

func (probe *workerBudgetProbe) create(ctx context.Context, id string, _ camoufoxnative.Options) (managedWorker, error) {
	probe.begin("start", id)
	worker := &budgetWorker{probe: probe, id: id, phase: aistudio.WorkerReady}
	var err error
	if probe.init != nil {
		err = probe.init(ctx, id)
	}
	probe.mu.Lock()
	probe.starting--
	probe.mu.Unlock()
	return worker, err
}

func (worker *budgetWorker) Prepare(ctx context.Context, request aistudio.ProtectedRequest) (aistudio.PreparedProtectedRequest, error) {
	return aistudio.PreparedProtectedRequest{Body: request.Body}, ctx.Err()
}

func (worker *budgetWorker) SendProtected(context.Context, aistudio.ProtectedRequest) (*aistudio.RPCResponse, error) {
	return nil, errors.New("offline worker does not send requests")
}

func (worker *budgetWorker) BrowserStorageState(context.Context) (aistudio.StorageState, error) {
	return aistudio.StorageState{}, errors.New("offline worker has no credentials")
}

func (*budgetWorker) MergeSetCookieHeaders(ctx context.Context, _ []string, _ string) error {
	return ctx.Err()
}

func (worker *budgetWorker) PageCatalog() camoufoxnative.PageCatalog {
	return camoufoxnative.PageCatalog{Logging: []byte("[]"), Tier: []byte("[]"), Models: []byte(worker.id)}
}

func (worker *budgetWorker) ProtocolHeaders(ctx context.Context, _ string) (http.Header, error) {
	if worker.probe.headers != nil {
		if err := worker.probe.headers(ctx, worker.id); err != nil {
			return nil, err
		}
	}
	worker.probe.mu.Lock()
	defer worker.probe.mu.Unlock()
	return http.Header{"X-Worker-Account": {worker.id}, "X-Worker-Revision": {strconv.Itoa(worker.revision)}}, ctx.Err()
}

func (worker *budgetWorker) Reload(ctx context.Context) error {
	worker.probe.begin("reload", worker.id)
	var err error
	if worker.probe.reload != nil {
		err = worker.probe.reload(ctx, worker.id)
	}
	worker.probe.mu.Lock()
	defer worker.probe.mu.Unlock()
	worker.probe.starting--
	worker.probe.live--
	if err == nil {
		worker.revision++
	}
	return err
}

func (worker *budgetWorker) State() aistudio.WorkerState {
	worker.probe.mu.Lock()
	defer worker.probe.mu.Unlock()
	return aistudio.WorkerState{AccountID: worker.id, Phase: worker.phase}
}

func (worker *budgetWorker) Close() error {
	if worker.probe.beforeClose != nil {
		worker.probe.beforeClose(worker.id)
	}
	worker.probe.mu.Lock()
	defer worker.probe.mu.Unlock()
	if worker.phase == aistudio.WorkerClosed {
		return nil
	}
	if worker.probe.closeFailures > 0 {
		worker.probe.closeFailures--
		worker.phase = aistudio.WorkerClosing
		return errors.New("offline close failure")
	}
	worker.probe.events = append(worker.probe.events, "close:"+worker.id)
	worker.probe.live--
	worker.phase = aistudio.WorkerClosed
	return nil
}

func newWorkerBudgetTest(t *testing.T, warm, active, startup, perAccount int) (*accountWorkerManager, *workerBudgetProbe) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	accounts := make([]*aistudio.Account, 0, 3)
	for _, id := range []string{"a@example.test", "b@example.test", "c@example.test"} {
		accounts = append(accounts, &aistudio.Account{ID: id, Config: aistudio.DefaultAccountConfig(id), State: aistudio.AccountReady})
	}
	pool := aistudio.NewAccountPool(accounts, perAccount)
	for _, account := range accounts {
		if err := pool.SetCatalog(account.ID, aistudio.BenefitTierFree, []aistudio.Model{{ID: "shared", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}}); err != nil {
			t.Fatal(err)
		}
	}
	manager := newAccountWorkerManager(pool, accounts, newRequestRegistry(t.Context()), "", "", 5*time.Second, warm, active, startup)
	probe := &workerBudgetProbe{}
	manager.newWorker = probe.create
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
		probe.mu.Lock()
		defer probe.mu.Unlock()
		if probe.live != 0 || probe.starting != 0 {
			t.Errorf("worker resources leaked: live=%d starting=%d", probe.live, probe.starting)
		}
	})
	return manager, probe
}

func assertWorkerBudget(t *testing.T, manager *accountWorkerManager, probe *workerBudgetProbe, live int) {
	t.Helper()
	probe.mu.Lock()
	if probe.live != live || probe.peak > manager.activeLimit() || probe.startupPeak > manager.startup.size() || probe.starting != 0 {
		t.Errorf("budget: live=%d want=%d peak=%d/%d startup=%d/%d starting=%d events=%v", probe.live, live, probe.peak, manager.activeLimit(), probe.startupPeak, manager.startup.size(), probe.starting, probe.events)
	}
	probe.mu.Unlock()
	manager.rebalanceMu.Lock()
	if len(manager.openings) != 0 {
		t.Errorf("openings leaked: %v", manager.openings)
	}
	manager.rebalanceMu.Unlock()
	manager.startup.mu.Lock()
	if manager.startup.active != 0 || manager.startup.waiting != 0 || len(manager.startup.reserved) != 0 {
		t.Errorf("startup budget leaked: active=%d waiting=%d reserved=%v", manager.startup.active, manager.startup.waiting, manager.startup.reserved)
	}
	manager.startup.mu.Unlock()
}

func workerBudgetResult[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("worker operation deadlocked")
		var zero T
		return zero
	}
}

func awaitWorkerBudget(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("worker scheduling did not reach the expected state")
		case <-ticker.C:
		}
	}
}

func assertRuntimeLeaseFree(t *testing.T, id string) {
	t.Helper()
	lease, err := aistudio.AcquireAccountRuntimeLease(id)
	if err != nil {
		t.Fatalf("runtime lease leaked for %s: %v", id, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerOpeningWaitDoesNotHideActiveRequests(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				account := &aistudio.Account{ID: "fixture", Config: aistudio.AccountConfig{Enabled: true}, State: aistudio.AccountReady}
				pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 3)
				leases := make([]*aistudio.AccountLease, 3)
				for index := range leases {
					lease, err := pool.AcquireFor(t.Context(), aistudio.AccountSelection{AccountID: account.ID})
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Release()
					leases[index] = lease
				}
				starter, waiter, active := leases[0], leases[1], leases[2]
				endRefresh, ok := starter.BeginAuthRefresh()
				if !ok {
					t.Fatal("could not begin authentication refresh")
				}
				defer endRefresh()
				generation := starter.AuthGeneration()
				opening := make(chan struct{})
				manager := &accountWorkerManager{openings: map[string]chan struct{}{account.ID: opening}}
				ctx, cancel := context.WithCancel(aistudio.ContextWithAccountLease(t.Context(), waiter))
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- manager.waitOpening(ctx, account.ID) }()
				synctest.Wait()
				check, stop := context.WithTimeout(t.Context(), time.Millisecond)
				err := starter.WaitForAuthRefresh(check, generation)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("opening wait hid an active request: %v", err)
				}
				if err := active.Release(); err != nil {
					t.Fatal(err)
				}
				if err := starter.WaitForAuthRefresh(t.Context(), generation); err != nil {
					t.Fatalf("opening waiter blocked authentication refresh: %v", err)
				}
				if canceled {
					cancel()
				} else {
					manager.rebalanceMu.Lock()
					manager.openings[account.ID] = make(chan struct{})
					close(opening)
					manager.rebalanceMu.Unlock()
				}
				if err := <-done; canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
					t.Fatalf("opening completion: %v", err)
				}
				check, stop = context.WithTimeout(t.Context(), time.Millisecond)
				defer stop()
				if err := starter.WaitForAuthRefresh(check, generation); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("finished opening left its lease excluded: %v", err)
				}
			})
		})
	}
}

func TestWorkerPageCatalogTemporaryBudget(t *testing.T) {
	for _, warm := range []int{0, 1} {
		t.Run(fmt.Sprintf("warm=%d", warm), func(t *testing.T) {
			manager, probe := newWorkerBudgetTest(t, warm, 1, 1, 1)
			if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
				t.Fatal(err)
			}
			catalog, err := manager.PageCatalog(t.Context(), "b@example.test", false)
			if err != nil || string(catalog.Models) != "b@example.test" {
				t.Fatalf("temporary catalog: %s, %v", catalog.Models, err)
			}
			assertWorkerBudget(t, manager, probe, 0)
			probe.mu.Lock()
			order := slices.Clone(probe.events)
			probe.mu.Unlock()
			if !slices.Equal(order, []string{"start:a@example.test", "close:a@example.test", "start:b@example.test", "close:b@example.test"}) {
				t.Fatalf("replacement overlapped old worker: %v", order)
			}
			headers, err := manager.ProtocolHeaders(t.Context(), "b@example.test")
			if err != nil || headers.Get("X-Worker-Account") != "b@example.test" {
				t.Fatalf("temporary headers were not retained: %v, %v", headers, err)
			}
			headers.Set("X-Worker-Account", "changed")
			if manager.lastPageHeaders("b@example.test").Get("X-Worker-Account") != "b@example.test" {
				t.Fatal("cached headers escaped by reference")
			}
			assertRuntimeLeaseFree(t, "a@example.test")
			assertRuntimeLeaseFree(t, "b@example.test")
		})
	}
}

func TestWorkerWarmTargetsAndCatalogReuse(t *testing.T) {
	for _, warm := range []int{0, 1} {
		t.Run(fmt.Sprintf("warm=%d", warm), func(t *testing.T) {
			manager, probe := newWorkerBudgetTest(t, warm, 1, 1, 1)
			if err := workerBudgetResult(t, manager.StartPrewarm(t.Context())); err != nil {
				t.Fatal(err)
			}
			manager.waitPrewarm()
			assertWorkerBudget(t, manager, probe, warm)
			if warm == 0 {
				if _, err := manager.PageCatalog(t.Context(), "a@example.test", false); err != nil {
					t.Fatal(err)
				}
				assertWorkerBudget(t, manager, probe, 0)
				return
			}
			id := manager.WarmAccountIDs()[0]
			for range 3 {
				if _, err := manager.PageCatalog(t.Context(), id, false); err != nil {
					t.Fatal(err)
				}
			}
			if manager.reapIdleWorker(time.Now().Add(time.Hour)) {
				t.Fatal("configured warm worker was reaped")
			}
			probe.mu.Lock()
			defer probe.mu.Unlock()
			if len(probe.events) != 1 {
				t.Fatalf("resident catalog started extra pages: %v", probe.events)
			}
		})
	}
}

func TestWorkerPageCatalogWaitsForBusyWorker(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.pool.AcquireFor(t.Context(), aistudio.AccountSelection{AccountID: "a@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.PageCatalog(ctx, "b@example.test", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("full catalog did not wait for capacity: %v", err)
	}
	assertWorkerBudget(t, manager, probe, 1)
	if evicted, err := manager.evictIdleWorker(t.Context(), "a@example.test"); err != nil || evicted {
		t.Fatalf("busy worker evicted: %v, %v", evicted, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PageCatalog(t.Context(), "b@example.test", false); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 0)
}

func TestWorkerConcurrentColdStartSameAccount(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	probe.init = func(ctx context.Context, _ string) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	result := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := manager.Worker(t.Context(), "a@example.test", "shared")
			result <- err
		}()
	}
	workerBudgetResult(t, started)
	close(release)
	for range 8 {
		if err := workerBudgetResult(t, result); err != nil {
			t.Fatal(err)
		}
	}
	assertWorkerBudget(t, manager, probe, 1)
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if !slices.Equal(probe.events, []string{"start:a@example.test"}) {
		t.Fatalf("same account opened multiple workers: %v", probe.events)
	}
}

func TestWorkerTemporaryPagesAndColdStartsShareBudget(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 2, 1, 1)
	started := make(chan string, 3)
	release := make(chan struct{})
	probe.init = func(ctx context.Context, id string) error {
		started <- id
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	result := make(chan error, 3)
	go func() { _, err := manager.PageCatalog(t.Context(), "a@example.test", false); result <- err }()
	if id := workerBudgetResult(t, started); id != "a@example.test" {
		t.Fatal(id)
	}
	go func() { _, err := manager.Worker(t.Context(), "b@example.test", "shared"); result <- err }()
	awaitWorkerBudget(t, func() bool {
		manager.startup.mu.Lock()
		defer manager.startup.mu.Unlock()
		return manager.startup.waiting > 0
	})
	go func() { _, err := manager.PageCatalog(t.Context(), "c@example.test", false); result <- err }()
	close(release)
	for range 3 {
		if err := workerBudgetResult(t, result); err != nil {
			t.Fatal(err)
		}
	}
	assertWorkerBudget(t, manager, probe, 1)
	if id := workerBudgetResult(t, started); id != "b@example.test" {
		t.Fatalf("temporary page bypassed ordinary startup: %s", id)
	}
}

func TestWorkerStartupCancellationReleasesReservation(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 2, 1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	probe.init = func(ctx context.Context, id string) error {
		if id != "a@example.test" {
			return errors.New("canceled startup should not reach the factory")
		}
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := manager.Worker(t.Context(), "a@example.test", "shared"); first <- err }()
	workerBudgetResult(t, started)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _, err := manager.Worker(ctx, "b@example.test", "shared"); second <- err }()
	awaitWorkerBudget(t, func() bool {
		manager.startup.mu.Lock()
		defer manager.startup.mu.Unlock()
		return manager.startup.waiting > 0
	})
	cancel()
	if err := workerBudgetResult(t, second); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	assertRuntimeLeaseFree(t, "b@example.test")
	close(release)
	if err := workerBudgetResult(t, first); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 1)
}

func TestWorkerTemporaryCancellationAndFailureReleaseBudget(t *testing.T) {
	for _, failure := range []string{"init", "headers", "cancel-init", "cancel-headers"} {
		t.Run(failure, func(t *testing.T) {
			manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
			started := make(chan struct{})
			fail := func(ctx context.Context, _ string) error {
				close(started)
				if failure == "cancel-init" || failure == "cancel-headers" {
					<-ctx.Done()
					return ctx.Err()
				}
				return errors.New("offline startup failure")
			}
			if failure == "headers" || failure == "cancel-headers" {
				probe.headers = fail
			} else {
				probe.init = fail
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := manager.PageCatalog(ctx, "a@example.test", false); result <- err }()
			workerBudgetResult(t, started)
			if failure == "cancel-init" || failure == "cancel-headers" {
				cancel()
			}
			if err := workerBudgetResult(t, result); err == nil {
				t.Fatal("failed page was accepted")
			}
			assertWorkerBudget(t, manager, probe, 0)
			assertRuntimeLeaseFree(t, "a@example.test")
			probe.init, probe.headers = nil, nil
			if _, err := manager.PageCatalog(t.Context(), "b@example.test", false); err != nil {
				t.Fatalf("failed page did not release capacity: %v", err)
			}
			assertWorkerBudget(t, manager, probe, 0)
		})
	}
}

func TestWorkerTemporaryCloseFailureKeepsBudget(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
	probe.closeFailures = 1
	if _, err := manager.PageCatalog(t.Context(), "a@example.test", false); err == nil {
		t.Fatal("temporary close failure was hidden")
	}
	assertWorkerBudget(t, manager, probe, 1)
	if manager.occupiedWorkers().slots != 1 {
		t.Fatal("failed cleanup disappeared from the activity budget")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.PageCatalog(ctx, "b@example.test", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("failed cleanup allowed a replacement: %v", err)
	}
	if err := manager.Reset("a@example.test"); err != nil {
		t.Fatal(err)
	}
	assertRuntimeLeaseFree(t, "a@example.test")
	if _, err := manager.PageCatalog(t.Context(), "b@example.test", false); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 0)
}

func TestWorkerBusyLeasesPreventReloadAndReplacement(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 1, 1, 1, 2)
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	busy, err := manager.pool.AcquireFor(t.Context(), aistudio.AccountSelection{AccountID: "a@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Release()
	if _, err := manager.PageCatalog(t.Context(), "a@example.test", true); !errors.Is(err, aistudio.ErrAccountLeased) {
		t.Fatalf("reload did not protect a busy lease: %v", err)
	}
	worker := manager.residentWorker("a@example.test").(*budgetWorker)
	probe.mu.Lock()
	worker.phase = aistudio.WorkerFailed
	probe.mu.Unlock()
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); !errors.Is(err, aistudio.ErrAccountLeased) {
		t.Fatalf("failed worker replacement closed a busy lease: %v", err)
	}
	assertWorkerBudget(t, manager, probe, 1)
	probe.mu.Lock()
	worker.phase = aistudio.WorkerReady
	probe.mu.Unlock()
	if err := busy.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PageCatalog(t.Context(), "a@example.test", true); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 1)
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if !slices.Equal(probe.events, []string{"start:a@example.test", "close:a@example.test", "start:a@example.test"}) {
		t.Fatalf("full reload did not close before restart: %v", probe.events)
	}
}

func TestWorkerReloadReservesExtraPage(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 1, 2, 1, 1)
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	probe.reload = func(ctx context.Context, _ string) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	result := make(chan error, 1)
	go func() { _, err := manager.PageCatalog(t.Context(), "a@example.test", true); result <- err }()
	workerBudgetResult(t, started)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.PageCatalog(ctx, "b@example.test", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("catalog bypassed the reload page budget: %v", err)
	}
	close(release)
	if err := workerBudgetResult(t, result); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 1)
	if manager.lastPageHeaders("a@example.test").Get("X-Worker-Revision") != "1" {
		t.Fatal("reload did not refresh protocol headers")
	}
}

func TestWorkerReloadCancellationAndFailureReleaseBudget(t *testing.T) {
	for _, active := range []int{1, 2} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("active=%d/canceled=%t", active, canceled), func(t *testing.T) {
				manager, probe := newWorkerBudgetTest(t, 1, active, 1, 1)
				if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
					t.Fatal(err)
				}
				started := make(chan struct{})
				fail := func(ctx context.Context, _ string) error {
					close(started)
					if canceled {
						<-ctx.Done()
						return ctx.Err()
					}
					return errors.New("offline reload failed")
				}
				probe.init, probe.reload = fail, fail
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				result := make(chan error, 1)
				go func() { _, err := manager.PageCatalog(ctx, "a@example.test", true); result <- err }()
				workerBudgetResult(t, started)
				if canceled {
					cancel()
				}
				if err := workerBudgetResult(t, result); err == nil {
					t.Fatal("failed reload was accepted")
				}
				assertWorkerBudget(t, manager, probe, 0)
				assertRuntimeLeaseFree(t, "a@example.test")
				probe.init, probe.reload = nil, nil
				if _, err := manager.Worker(t.Context(), "b@example.test", "shared"); err != nil {
					t.Fatal(err)
				}
				assertWorkerBudget(t, manager, probe, 1)
			})
		}
	}
}

func TestWorkerReloadCloseFailureKeepsExtraBudget(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 1, 2, 1, 1)
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	probe.reload = func(context.Context, string) error { return errors.New("offline reload failed") }
	probe.closeFailures = 1
	if _, err := manager.PageCatalog(t.Context(), "a@example.test", true); err == nil {
		t.Fatal("reload cleanup failure was hidden")
	}
	if manager.occupiedWorkers().slots != 2 {
		t.Fatal("failed reload lost its extra page reservation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.PageCatalog(ctx, "b@example.test", false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("failed reload allowed another page: %v", err)
	}
	if err := manager.Reset("a@example.test"); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 0)
	assertRuntimeLeaseFree(t, "a@example.test")
	if manager.occupiedWorkers().slots != 0 {
		t.Fatal("successful cleanup retained the extra page reservation")
	}
}

func TestWorkerResetAllCancelsTemporaryStartup(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
	started := make(chan struct{})
	probe.init = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := manager.PageCatalog(t.Context(), "a@example.test", false); result <- err }()
	workerBudgetResult(t, started)
	reset := make(chan error, 1)
	go func() { reset <- manager.ResetAll() }()
	if err := workerBudgetResult(t, reset); err != nil {
		t.Fatal(err)
	}
	if err := workerBudgetResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("reset left temporary startup running: %v", err)
	}
	assertWorkerBudget(t, manager, probe, 0)
	assertRuntimeLeaseFree(t, "a@example.test")
}

func TestWorkerPrewarmFailureKeepsConfiguredBudget(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 1, 1, 1, 1)
	probe.init = func(_ context.Context, id string) error {
		if id == "a@example.test" {
			return errors.New("offline cold-start failure")
		}
		return nil
	}
	if err := workerBudgetResult(t, manager.StartPrewarm(t.Context())); err != nil {
		t.Fatal(err)
	}
	manager.waitPrewarm()
	assertWorkerBudget(t, manager, probe, 1)
	assertRuntimeLeaseFree(t, "a@example.test")
	if manager.warmLimit() != 1 || len(manager.WarmAccountIDs()) != 1 {
		t.Fatal("cold-start failure changed the configured warm target")
	}
}

func TestWorkerReplacementWaitsForOldClose(t *testing.T) {
	manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
	if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	closing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	probe.beforeClose = func(id string) {
		if id == "a@example.test" {
			once.Do(func() { close(closing); <-release })
		}
	}
	defer close(release)
	result := make(chan error, 1)
	go func() { _, err := manager.Worker(t.Context(), "b@example.test", "shared"); result <- err }()
	workerBudgetResult(t, closing)
	probe.mu.Lock()
	if !slices.Equal(probe.events, []string{"start:a@example.test"}) || probe.live != 1 {
		t.Errorf("replacement started before old worker stopped: %v", probe.events)
	}
	probe.mu.Unlock()
	// Let Close finish without double-closing the cleanup channel.
	release <- struct{}{}
	if err := workerBudgetResult(t, result); err != nil {
		t.Fatal(err)
	}
	assertWorkerBudget(t, manager, probe, 1)
}

func TestWorkerCloseFailureCanBeRetried(t *testing.T) {
	for _, operation := range []string{"close", "remove"} {
		t.Run(operation, func(t *testing.T) {
			manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
			if _, err := manager.Worker(t.Context(), "a@example.test", "shared"); err != nil {
				t.Fatal(err)
			}
			stop := manager.Close
			if operation == "remove" {
				stop = func() error { return manager.Remove("a@example.test") }
			}
			probe.closeFailures = 1
			if err := stop(); err == nil {
				t.Fatal("manager hid the close failure")
			}
			if manager.occupiedWorkers().slots != 1 {
				t.Fatal("manager dropped a failed close from capacity accounting")
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			assertWorkerBudget(t, manager, probe, 0)
			assertRuntimeLeaseFree(t, "a@example.test")
		})
	}
}
