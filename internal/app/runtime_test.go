package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

type retryTestService struct {
	aistudio.Service
	generate func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error)
}

type videoScopeTestService struct {
	aistudio.Service
	aistudio.VideoService
	taskContext func() (context.Context, context.CancelFunc, error)
}

func (service *videoScopeTestService) GenerateVideo(_ context.Context, request aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	service.taskContext = request.TaskContext
	return aistudio.VideoOperation{ID: "offline-video"}, nil
}

func (service retryTestService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	return service.generate(ctx, request)
}

func retryTestRuntime(t *testing.T, generate func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error)) *trackedService {
	t.Helper()
	accounts := []*aistudio.Account{
		{ID: "a@example.test", Config: aistudio.DefaultAccountConfig("a@example.test"), State: aistudio.AccountReady},
		{ID: "b@example.test", Config: aistudio.DefaultAccountConfig("b@example.test"), State: aistudio.AccountReady},
	}
	pool := aistudio.NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]aistudio.Channel{aistudio.ChannelPlayground, aistudio.ChannelBuild})
	models := []aistudio.Model{{ID: "shared", Methods: []string{"generateContent"}}}
	for _, account := range accounts {
		if err := pool.SetCatalog(account.ID, aistudio.BenefitTierFree, models); err != nil {
			t.Fatal(err)
		}
		if err := pool.SetBuildCatalog(account.ID, models); err != nil {
			t.Fatal(err)
		}
	}
	return &trackedService{
		lifecycle: t.Context(), service: retryTestService{generate: generate}, pool: pool,
		requests: newRequestRegistry(t.Context()), workers: &accountWorkerManager{},
		maxAccountSwitches: config.DefaultMaxAccountSwitches,
	}
}

func assertRetryLeasesReleased(t *testing.T, service *trackedService) {
	t.Helper()
	if service.requests.count() != 0 {
		t.Fatal("request remained active")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for _, account := range []string{"a@example.test", "b@example.test"} {
		lease, err := service.pool.AcquireAccount(ctx, account)
		if err != nil {
			t.Fatalf("account lease not released: %s: %v", account, err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerateWithRetryHonorsAccountSwitchBudget(t *testing.T) {
	for _, test := range []struct {
		name                       string
		limit, coldFailures, calls int
	}{
		{name: "disabled", limit: 0, calls: 1},
		{name: "one switch", limit: 1, calls: 2},
		{name: "cold start used budget", limit: 1, coldFailures: 1, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			service := retryTestRuntime(t, func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
				calls++
				return nil, &url.Error{Op: "Post", URL: "https://example.test", Err: io.ErrUnexpectedEOF}
			})
			service.maxAccountSwitches = test.limit
			ctx, cancel := context.WithTimeout(aistudio.ContextWithChannel(t.Context(), aistudio.ChannelPlayground), 5*time.Second)
			defer cancel()
			request := aistudio.GenerateRequest{ID: "switch-budget", Model: "shared"}
			service.requests.start(request, cancel)
			acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool, failures *int, _ *time.Duration) (*aistudio.AccountLease, error) {
				if calls == 0 {
					*failures += test.coldFailures
				}
				return service.pool.AcquireFor(ctx, selection)
			}
			events := make(chan aistudio.Event)
			go service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
			for range events {
			}
			if calls != test.calls {
				t.Fatalf("switch budget allowed %d attempts, want %d", calls, test.calls)
			}
			assertRetryLeasesReleased(t, service)
		})
	}
}

func TestGenerateWithRetryStreamInterruption(t *testing.T) {
	interrupted := &aistudio.StreamInterruptedError{Method: "GenerateContent", Frames: 1, Err: io.ErrUnexpectedEOF}
	protocol := &aistudio.ProtocolEvidenceError{Method: "GenerateContent", Path: "$", Detail: "malformed response"}
	for _, channel := range []aistudio.Channel{aistudio.ChannelPlayground, aistudio.ChannelBuild} {
		for _, tc := range []struct {
			name        string
			unary       bool
			beforeFirst bool
			failAll     bool
			fixed       bool
			failure     error
			wantCalls   int
			wantText    string
			wantError   bool
		}{
			{name: "unary-retries", unary: true, failure: interrupted, wantCalls: 2, wantText: "success"},
			{name: "stream-does-not-replay", failure: interrupted, wantCalls: 1, wantText: "discard-me", wantError: true},
			{name: "protocol-error-is-not-retried", unary: true, failure: protocol, wantCalls: 1, wantText: "discard-me", wantError: true},
			{name: "exhausted-attempts-discard-partials", unary: true, failAll: true, failure: interrupted, wantCalls: 2, wantError: true},
			{name: "fixed-account-does-not-switch", unary: true, fixed: true, failure: interrupted, wantCalls: 1, wantError: true},
			{name: "transport-error-before-first", unary: true, beforeFirst: true, failure: &url.Error{Op: "Post", URL: "https://example.test", Err: io.ErrUnexpectedEOF}, wantCalls: 2, wantText: "success"},
			{name: "bad-request-does-not-cross-channels", unary: true, beforeFirst: true, failure: &aistudio.RPCError{StatusCode: 400, Message: "INVALID_ARGUMENT"}, wantCalls: 1, wantError: true},
			{name: "upstream-5xx-retries-same-channel", unary: true, beforeFirst: true, failure: &aistudio.RPCError{StatusCode: 503, Message: "UNAVAILABLE"}, wantCalls: 2, wantText: "success"},
		} {
			t.Run(string(channel)+"/"+tc.name, func(t *testing.T) {
				var calls []string
				service := retryTestRuntime(t, func(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					lease, ok := aistudio.AccountLeaseFromContext(ctx)
					if !ok || lease.Channel() != channel || lease.Account().ID != request.AccountID {
						return nil, fmt.Errorf("request lost its account or fixed channel")
					}
					calls = append(calls, request.AccountID)
					failed := len(calls) == 1 || tc.failAll
					if failed && tc.beforeFirst {
						return nil, tc.failure
					}
					events := make(chan aistudio.Event, 2)
					if failed {
						events <- aistudio.Event{Kind: aistudio.EventText, Text: "discard-me", ProviderModel: "shared"}
						events <- aistudio.Event{Kind: aistudio.EventError, Err: tc.failure}
					} else {
						events <- aistudio.Event{Kind: aistudio.EventText, Text: "success", ProviderModel: "shared"}
						events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
					}
					close(events)
					return events, nil
				})
				ctx, cancel := context.WithTimeout(aistudio.ContextWithChannel(t.Context(), channel), 5*time.Second)
				defer cancel()
				request := aistudio.GenerateRequest{ID: "retry-test", Model: "shared", Unary: tc.unary}
				if tc.fixed {
					request.AccountID = "a@example.test"
				}
				service.requests.start(request, cancel)
				events := make(chan aistudio.Event)
				acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool, _ *int, _ *time.Duration) (*aistudio.AccountLease, error) {
					return service.pool.AcquireFor(ctx, selection)
				}
				go service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
				var text strings.Builder
				var responseErr error
				for event := range events {
					if event.Kind == aistudio.EventText {
						text.WriteString(event.Text)
					}
					if event.Kind == aistudio.EventError {
						responseErr = event.Err
					}
				}
				if len(calls) != tc.wantCalls || text.String() != tc.wantText || (responseErr != nil) != tc.wantError {
					t.Fatalf("calls=%v text=%q error=%v", calls, text.String(), responseErr)
				}
				if tc.wantError && !errors.Is(responseErr, tc.failure) {
					t.Fatalf("unexpected failure: %v", responseErr)
				}
				if len(calls) == 2 && calls[0] == calls[1] {
					t.Fatalf("retry reused failed account: %v", calls)
				}
				assertRetryLeasesReleased(t, service)
			})
		}
	}
}

func TestGenerateWithRetryCancellation(t *testing.T) {
	for _, unary := range []bool{false, true} {
		t.Run(fmt.Sprintf("unary=%t", unary), func(t *testing.T) {
			firstSent := make(chan struct{})
			cleaned := make(chan struct{})
			calls := 0
			service := retryTestRuntime(t, func(ctx context.Context, _ aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
				calls++
				events := make(chan aistudio.Event)
				go func() {
					defer close(cleaned)
					defer close(events)
					select {
					case events <- aistudio.Event{Kind: aistudio.EventText, Text: "first"}:
						close(firstSent)
					case <-ctx.Done():
						return
					}
					<-ctx.Done()
				}()
				return events, nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request := aistudio.GenerateRequest{ID: "cancel-test", Model: "shared", Unary: unary}
			service.requests.start(request, cancel)
			events := make(chan aistudio.Event)
			acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool, _ *int, _ *time.Duration) (*aistudio.AccountLease, error) {
				return service.pool.AcquireFor(ctx, selection)
			}
			go service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
			select {
			case <-firstSent:
			case <-ctx.Done():
				t.Fatal("first event was not consumed")
			}
			// A streaming request is blocked on its consumer; a unary request is collecting.
			cancel()
			for range events {
			}
			select {
			case <-cleaned:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream producer was not canceled")
			}
			if calls != 1 {
				t.Fatalf("canceled request retried: %d calls", calls)
			}
			assertRetryLeasesReleased(t, service)
		})
	}
}

func TestCollectGenerateEventsCancellationDuringDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := make(chan aistudio.Event)
	defer close(source)
	done := make(chan error, 1)
	go func() {
		_, _, err := collectGenerateEvents(ctx, source, nil)
		done <- err
	}()
	source <- aistudio.Event{Kind: aistudio.EventError, Err: &aistudio.StreamInterruptedError{Err: io.ErrUnexpectedEOF}}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("drain ignored cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain waited indefinitely for upstream closure")
	}
}

func TestGenerateWithRetryQuotaFallbackKeepsChannel(t *testing.T) {
	for _, channel := range []aistudio.Channel{aistudio.ChannelPlayground, aistudio.ChannelBuild} {
		for name, message := range map[string]string{
			"plan":  "You have reached your usage limit, resets in 1 hour",
			"daily": "You have reached your usage limit; try again tomorrow",
		} {
			t.Run(string(channel)+"/"+name, func(t *testing.T) {
				limit := &aistudio.RPCError{StatusCode: 429, Message: message}
				service := retryTestRuntime(t, func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					return nil, limit
				})
				service.maxAccountSwitches = 0 // Quota failures do not consume the non-quota switch budget.
				ctx, cancel := context.WithTimeout(aistudio.ContextWithChannel(t.Context(), channel), 5*time.Second)
				defer cancel()
				request := aistudio.GenerateRequest{ID: "quota-limit-test", Model: "shared"}
				service.requests.start(request, cancel)
				var fallbacks []bool
				acquire := func(ctx context.Context, selection aistudio.AccountSelection, quotaFallback bool, _ *int, _ *time.Duration) (*aistudio.AccountLease, error) {
					fallbacks = append(fallbacks, quotaFallback)
					if selection.Channel != channel {
						return nil, errors.New("quota fallback lost the fixed URL channel")
					}
					return service.pool.AcquireFor(ctx, selection)
				}
				events := make(chan aistudio.Event)
				go service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
				for range events {
				}
				if !slices.Equal(fallbacks, []bool{true, true}) {
					t.Fatalf("quota failure did not try each candidate on the fixed channel: %v", fallbacks)
				}
				assertRetryLeasesReleased(t, service)
			})
		}
	}
}

func TestForwardTerminalErrorFinishesRequestBeforeDrain(t *testing.T) {
	service := retryTestRuntime(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	lease, err := service.pool.AcquireFor(ctx, aistudio.AccountSelection{AccountID: "a@example.test", ModelID: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	request := aistudio.GenerateRequest{ID: "terminal-before-drain", Model: "shared"}
	service.requests.start(request, cancel)
	source := make(chan aistudio.Event, 1)
	source <- aistudio.Event{Kind: aistudio.EventError, Err: errors.New("offline terminal error"), AntiTruncationToolCalls: 2}
	destination := make(chan aistudio.Event)
	var serving atomic.Pointer[aistudio.AccountLease]
	serving.Store(lease)
	go service.forwardEvents(ctx, ctx, cancel, request.ID,
		aistudio.Event{Kind: aistudio.EventText, Text: "partial"}, source, destination, &serving, nil, &upstreamActivity{}, request.Model)
	if event := <-destination; event.Kind != aistudio.EventText {
		t.Fatalf("first event=%+v", event)
	}
	if event := <-destination; event.Kind != aistudio.EventError {
		t.Fatalf("terminal event=%+v", event)
	}
	if service.requests.count() != 0 {
		t.Fatal("terminal error reached the client before the request was finished")
	}
	busyCtx, stopWaiting := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer stopWaiting()
	if other, err := service.pool.AcquireAccount(busyCtx, "a@example.test"); other != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("account was released before the upstream drained: lease=%v err=%v", other, err)
	}
	close(source)
	for range destination {
	}
	assertRetryLeasesReleased(t, service)
}

func TestDispatchQueueDoesNotBlockIndependentAPIKeyScopes(t *testing.T) {
	manager, _ := newWorkerBudgetTest(t, 2, 2, 1, 1)
	pool := manager.pool
	pool.SetAPIKeyAccounts(map[string][]string{"key-a": {"a@example.test"}, "key-b": {"b@example.test"}})
	selection := aistudio.AccountSelection{ModelID: "shared", Method: "generateContent"}
	unrestricted := dispatchKey(selection)
	selection.AllowedAccountIDs = []string{}
	if dispatchKey(selection) == unrestricted {
		t.Fatal("explicit empty scope shares a dispatch queue with unrestricted requests")
	}
	selection.AllowedAccountIDs = nil
	if _, err := manager.Worker(t.Context(), "b@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	busy, err := pool.AcquireFor(t.Context(), aistudio.AccountSelection{AccountID: "a@example.test", ModelID: "shared", Method: "generateContent"})
	if err != nil {
		t.Fatal(err)
	}
	service := &trackedService{pool: pool, workers: manager, requests: manager.requests}
	ctxA, cancelA := context.WithCancel(aistudio.ContextWithAPIKey(t.Context(), "key-a"))
	first := make(chan error, 1)
	go func() {
		failures, cooled := 0, time.Duration(0)
		lease, err := service.acquireWarmLease(ctxA, selection, false, &failures, &cooled)
		if lease != nil {
			err = errors.Join(err, lease.Release())
		}
		first <- err
	}()
	t.Cleanup(func() {
		cancelA()
		_ = busy.Release()
		select {
		case <-first:
		case <-time.After(5 * time.Second):
			t.Error("canceled queued request did not stop")
		}
	})
	awaitWorkerBudget(t, func() bool {
		manager.dispatch.mu.Lock()
		defer manager.dispatch.mu.Unlock()
		return len(manager.dispatch.waiters) == 1
	})
	ctxB, cancelB := context.WithTimeout(aistudio.ContextWithAPIKey(t.Context(), "key-b"), time.Second)
	defer cancelB()
	failures, cooled := 0, time.Duration(0)
	lease, err := service.acquireWarmLease(ctxB, selection, false, &failures, &cooled)
	if err != nil {
		t.Fatalf("unrelated key was blocked behind a busy scoped account: %v", err)
	}
	if lease.Account().ID != "b@example.test" {
		t.Fatal("queued request escaped its API key scope")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchQueueRekeysLiveAPIKeyBindings(t *testing.T) {
	manager, _ := newWorkerBudgetTest(t, 2, 2, 1, 1)
	pool := manager.pool
	allowed := []string{"a@example.test", "b@example.test"}
	pool.SetAPIKeyAccounts(map[string][]string{"key-a": allowed, "key-b": allowed})
	if _, err := manager.Worker(t.Context(), "b@example.test", "shared"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	busy := make([]*aistudio.AccountLease, 0, len(allowed))
	type result struct {
		key, account string
		err          error
	}
	results := make(chan result, 2)
	started, received := 0, 0
	t.Cleanup(func() {
		cancel()
		for _, lease := range busy {
			_ = lease.Release()
		}
		for range started - received {
			workerBudgetResult(t, results)
		}
	})
	for _, id := range allowed {
		lease, err := pool.AcquireFor(ctx, aistudio.AccountSelection{AccountID: id, ModelID: "shared", Method: "generateContent"})
		if err != nil {
			t.Fatal(err)
		}
		busy = append(busy, lease)
	}
	service := &trackedService{pool: pool, workers: manager, requests: manager.requests}
	selection := aistudio.AccountSelection{ModelID: "shared", Method: "generateContent"}
	for _, key := range []string{"key-a", "key-b"} {
		started++
		go func() {
			requestCtx := aistudio.ContextWithAccountScope(aistudio.ContextWithAPIKey(ctx, key), allowed)
			failures, cooled := 0, time.Duration(0)
			lease, err := service.acquireWarmLease(requestCtx, selection, false, &failures, &cooled)
			value := result{key: key, err: err}
			if lease != nil {
				value.account = lease.Account().ID
				value.err = errors.Join(value.err, lease.Release())
			}
			results <- value
		}()
		awaitWorkerBudget(t, func() bool {
			manager.dispatch.mu.Lock()
			defer manager.dispatch.mu.Unlock()
			return len(manager.dispatch.waiters) == started
		})
	}
	pool.SetAPIKeyAccounts(map[string][]string{"key-a": {allowed[0]}, "key-b": {allowed[1]}})
	if err := busy[1].Release(); err != nil {
		t.Fatal(err)
	}
	completed := workerBudgetResult(t, results)
	received++
	if completed.err != nil || completed.key != "key-b" || completed.account != allowed[1] {
		t.Fatalf("live scope update did not unblock the independent ready account: %+v", completed)
	}
	select {
	case value := <-results:
		received++
		t.Fatalf("the still-busy account unexpectedly completed: %+v", value)
	default:
	}
}

func TestDispatchQueuePreservesFIFOAcrossLiveScopeChanges(t *testing.T) {
	pool := aistudio.NewAccountPool(nil, 1)
	pool.SetAPIKeyAccounts(map[string][]string{"first": {"a", "b"}, "second": {"a", "b"}})
	queue := newDispatchQueue(pool)
	selection := aistudio.AccountSelection{ModelID: "shared", Method: "generateContent"}
	first := queue.join(aistudio.ContextWithAPIKey(t.Context(), "first"), selection)
	second := queue.join(aistudio.ContextWithAPIKey(t.Context(), "second"), selection)
	for _, scopes := range []map[string][]string{
		{"first": {"a"}, "second": {"a"}},
		{"first": {"a", "b"}, "second": {"a", "b"}},
	} {
		pool.SetAPIKeyAccounts(scopes)
		if queue.turn(second, selection) || !queue.turn(first, selection) {
			t.Fatal("later waiter overtook the original head after a common scope change")
		}
	}
	queue.setStarting(first, true)
	if !queue.turn(second, selection) {
		t.Fatal("a starting worker blocked the next request")
	}
	queue.setStarting(first, false)
	queue.leave(first)
	if !queue.turn(second, selection) {
		t.Fatal("removing the head did not advance the queue")
	}
	queue.leave(second)
}

type dispatchScopeBarrier struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (barrier *dispatchScopeBarrier) Value(key any) any {
	barrier.once.Do(func() {
		close(barrier.entered)
		<-barrier.resume
	})
	return barrier.Context.Value(key)
}

func TestDispatchQueueUsesOneBindingSnapshotPerTurn(t *testing.T) {
	pool := aistudio.NewAccountPool(nil, 1)
	pool.SetAPIKeyAccounts(map[string][]string{"first": {"a", "b"}, "second": {"a", "b"}})
	queue := newDispatchQueue(pool)
	selection := aistudio.AccountSelection{ModelID: "shared", Method: "generateContent"}
	barrier := &dispatchScopeBarrier{
		Context: aistudio.ContextWithAPIKey(t.Context(), "first"), entered: make(chan struct{}), resume: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(barrier.resume) })
	t.Cleanup(release)
	queue.join(barrier, selection)
	second := queue.join(aistudio.ContextWithAPIKey(t.Context(), "second"), selection)
	result := make(chan bool, 1)
	go func() { result <- queue.turn(second, selection) }()
	workerBudgetResult(t, barrier.entered)
	pool.SetAPIKeyAccounts(map[string][]string{"first": {"a"}, "second": {"a"}})
	release()
	if workerBudgetResult(t, result) || queue.turn(second, selection) {
		t.Fatal("mixed binding epochs let a later equivalent request overtake the head")
	}
}

type assistantLifecycleFixture struct {
	page camoufoxnative.PageCatalog
	send func(context.Context, aistudio.RPCRequest) (*aistudio.RPCResponse, error)
}

func (fixture *assistantLifecycleFixture) PageCatalog(ctx context.Context, _ string, _ bool) (camoufoxnative.PageCatalog, error) {
	return fixture.page, ctx.Err()
}

func (fixture *assistantLifecycleFixture) Do(ctx context.Context, rpc aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	return fixture.send(ctx, rpc)
}

func (fixture *assistantLifecycleFixture) DoProtected(ctx context.Context, _ aistudio.GenerateRequest, rpc aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	return fixture.send(ctx, rpc)
}

func (fixture *assistantLifecycleFixture) DoProtectedApplet(ctx context.Context, _ aistudio.AppletProtectedRequest, rpc aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	return fixture.send(ctx, rpc)
}

func TestServiceStopJoinsAssistantCompanionsBeforeWorkerReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, probe := newWorkerBudgetTest(t, 0, 1, 1, 1)
		row := make([]any, 65)
		row[0], row[3], row[5], row[6] = "models/shared", "Shared", 1024, 256
		row[7], row[64] = []string{"generateContent", "countTokens"}, []int{1, 48}
		catalog, err := json.Marshal([]any{[]any{row}})
		if err != nil {
			t.Fatal(err)
		}
		step := make([]any, 16)
		step[0] = []any{[]any{[]any{[]any{nil, "complete answer"}}, "model"}}
		step[11], step[12], step[15] = 1, 1, []int{4, 2, 1}
		stream, err := json.Marshal([]any{[]any{[]any{nil, nil, []any{step}, "trajectory"}}})
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan context.Context, 1)
		resume := make(chan struct{})
		release := sync.OnceFunc(func() { close(resume) })
		fixture := &assistantLifecycleFixture{page: camoufoxnative.PageCatalog{Logging: []byte("[]"), Tier: []byte("[1]"), Models: catalog}}
		fixture.send = func(ctx context.Context, rpc aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
			body := "[]"
			switch rpc.Method {
			case "ListCloudApiKeys":
				body = `[[[null,null,"fixture-key"]]]`
			case "CreateApplet":
				body = `[null,["applet",null,null,["Title","Description"]]]`
			case "CodeAssistantOffline":
				body = `["task","trajectory"]`
			case "StreamCodeAssistantOfflineGeneration":
				body = string(stream)
			case "ProvisionAndInitializeApplet":
				entered <- ctx
				<-resume
				_, err := manager.Worker(ctx, rpc.AccountID, "shared")
				if !errors.Is(err, context.Canceled) {
					t.Errorf("stopped assistant companion could recreate a worker: %v", err)
				}
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &aistudio.RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {aistudio.JSONProtobufContentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		client := aistudio.NewClient(aistudio.ClientOptions{Transport: fixture, Protected: fixture, Pages: fixture})
		pooled := aistudio.NewPooledService(manager.pool, client)
		for _, id := range manager.pool.EnabledAccounts() {
			if _, err := pooled.RefreshAccountModels(t.Context(), id, false); err != nil {
				t.Fatal(err)
			}
		}
		service := newTrackedService(t.Context(), pooled, manager.pool, manager.requests, manager, time.Minute)
		t.Cleanup(func() {
			release()
			_, _ = service.Stop()
		})
		if _, _, err := service.Start(t.Context(), func() {}); err != nil {
			t.Fatal(err)
		}
		firstLifecycle := service.assistantLifecycle
		requestCtx, cancel, err := service.dataRequestContext(aistudio.ContextWithChannel(t.Context(), aistudio.ChannelBuild))
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
		request := aistudio.GenerateRequest{AccountID: "a@example.test", Model: "shared-build", Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "reply"}}}}}
		events, err := pooled.Generate(requestCtx, request)
		if err != nil {
			t.Fatal(err)
		}
		for event := range events {
			if event.Kind == aistudio.EventError {
				t.Fatal(event.Err)
			}
		}
		companionCtx := workerBudgetResult(t, entered)
		stopped := make(chan error, 1)
		go func() { _, err := service.Stop(); stopped <- err }()
		workerBudgetResult(t, companionCtx.Done())
		synctest.Wait()
		select {
		case err := <-stopped:
			t.Fatalf("Stop returned before its retained companion was drained: %v", err)
		default:
		}
		release()
		if err := workerBudgetResult(t, stopped); err != nil {
			t.Fatal(err)
		}
		if service.assistantLifecycle != nil {
			t.Fatal("stopped service retained its active assistant scope")
		}
		assertWorkerBudget(t, manager, probe, 0)
		for _, status := range manager.pool.Status() {
			if status.ActiveRequests != 0 {
				t.Fatalf("assistant cleanup leaked an account lease: %+v", status)
			}
		}
		if _, _, err := service.Start(t.Context(), func() {}); err != nil || service.assistantLifecycle == firstLifecycle || service.assistantLifecycle == nil {
			t.Fatalf("restart did not create an independent assistant scope: %v", err)
		}
		if _, err := pooled.Generate(context.WithoutCancel(requestCtx), request); !errors.Is(err, context.Canceled) {
			t.Fatalf("request admitted before Stop escaped into the new generation: %v", err)
		}
	})
}

func TestVideoTaskPreservesRequestScope(t *testing.T) {
	for _, channel := range []aistudio.Channel{aistudio.ChannelPlayground, aistudio.ChannelBuild} {
		t.Run(string(channel), func(t *testing.T) {
			service := retryTestRuntime(t, nil)
			video := &videoScopeTestService{}
			service.service = video
			service.timeout = 5 * time.Second
			dataCtx, stopData := context.WithCancel(t.Context())
			defer stopData()
			service.dataContext = dataCtx
			service.dataCancel = stopData
			service.workers = newAccountWorkerManager(service.pool, nil, service.requests, "", "", time.Second, 0, 1, 1)
			t.Cleanup(func() {
				if err := service.workers.Close(); err != nil {
					t.Error(err)
				}
			})
			service.state.Store(serviceRunning)
			service.pool.SetAPIKeyAccounts(map[string][]string{"limited": {"a@example.test"}})
			model := aistudio.Model{ID: "shared", Methods: []string{"generateContent"}, CapabilityOptions: map[string][]string{"video_output_resolutions": {"720p"}}}
			for _, id := range []string{"a@example.test", "b@example.test"} {
				if err := service.pool.SetCatalog(id, aistudio.BenefitTierFree, []aistudio.Model{model}); err != nil {
					t.Fatal(err)
				}
				if err := service.pool.SetBuildCatalog(id, []aistudio.Model{model}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancelRequest := context.WithCancel(aistudio.ContextWithAPIKey(aistudio.ContextWithChannel(t.Context(), channel), "limited"))
			defer cancelRequest()
			if _, err := service.GenerateVideo(ctx, aistudio.VideoRequest{Model: model.ID, Prompt: "offline test"}); err != nil {
				t.Fatal(err)
			}
			cancelRequest()
			taskCtx, cancelTask, err := video.taskContext()
			if err != nil {
				t.Fatal(err)
			}
			defer cancelTask()
			if taskCtx.Err() != nil || aistudio.ChannelFromContext(taskCtx) != channel {
				t.Fatalf("background task lost URL scope or inherited cancellation: channel=%q err=%v", aistudio.ChannelFromContext(taskCtx), taskCtx.Err())
			}
			selection := aistudio.AccountSelection{ModelID: model.ID, Method: "generateContent"}
			lease, err := service.pool.AcquireFor(taskCtx, selection)
			if err != nil || lease == nil || lease.Account().ID != "a@example.test" || lease.Channel() != channel {
				t.Fatalf("background task lost request scope: lease=%v err=%v", lease, err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			groups, err := service.pool.ClassifyCandidates(taskCtx, selection, []string{"a@example.test", "b@example.test"})
			if err != nil || !slices.Equal(groups.WarmReady, []string{"a@example.test"}) {
				t.Fatalf("background worker selection escaped key scope: groups=%+v err=%v", groups, err)
			}
			selection.AllowedAccountIDs = []string{"b@example.test"}
			if lease, err := service.pool.AcquireFor(taskCtx, selection); lease != nil || !errors.Is(err, aistudio.ErrNoEligibleAccount) {
				t.Fatalf("background retry escaped key scope: lease=%v err=%v", lease, err)
			}
			if lease, waitable, err := service.pool.TryAcquireFor(taskCtx, selection); lease != nil || waitable || err != nil {
				t.Fatalf("background try-acquire escaped key scope: lease=%v err=%v", lease, err)
			}
			if stopped, err := service.Stop(); !stopped || err != nil {
				t.Fatalf("stopping the service failed: stopped=%v err=%v", stopped, err)
			}
			select {
			case <-taskCtx.Done():
			case <-time.After(time.Second):
				t.Fatal("stopping the data service did not cancel the background task")
			}
		})
	}
}
