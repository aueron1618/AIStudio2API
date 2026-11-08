package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
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
				acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool) (*aistudio.AccountLease, error) {
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
			acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool) (*aistudio.AccountLease, error) {
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

func TestGenerateWithRetryQuotaFallbackOnce(t *testing.T) {
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
				ctx, cancel := context.WithTimeout(aistudio.ContextWithChannel(t.Context(), channel), 5*time.Second)
				defer cancel()
				request := aistudio.GenerateRequest{ID: "quota-limit-test", Model: "shared"}
				service.requests.start(request, cancel)
				var fallbacks []bool
				acquire := func(ctx context.Context, selection aistudio.AccountSelection, quotaFallback bool) (*aistudio.AccountLease, error) {
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
				if !slices.Equal(fallbacks, []bool{true, false}) {
					t.Fatalf("quota failure enabled another fallback or changed channels: %v", fallbacks)
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
	go service.forwardEvents(ctx, ctx, cancel, request.ID,
		aistudio.Event{Kind: aistudio.EventText, Text: "partial"}, source, destination, lease, nil, &upstreamActivity{}, request.Model)
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
