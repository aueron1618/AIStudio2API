package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

const pageFixtureModels = `[[["models/fixture",null,"v1","Fixture",null,1024,256,["generateContent"]]]]`

const pageFixtureBundle = "https://www.gstatic.com/boq-makersuite/_/js/k=boq-makersuite.MakerSuite.en_US.fixture/exm=_b/m=_b"

const pageFixtureDocument = `<html lang="en"><head><script>var flags = {"WIu0Nc":"fixture-key"};</script>
<script type="application/json">{"cfb2h":"fixture","goX6ee":[1],"TSDtV":"%.@.[[null,null,\"fixture\"]]]","UUFaWc":"%.@.1]","S6lZl":1}</script>
<link rel="stylesheet" href="/styles.css"><link rel="preload" as="script" href="/bundle.js"><script src="/bundle.js"></script>
<link rel="preload" as="script" href="` + pageFixtureBundle + `"><script src="` + pageFixtureBundle + `"></script>
<link rel="icon" href="/icon.png" sizes="16x16"></head><body></body></html>`

const pageFixtureInterpreter = `var fixture = {a(program, ready) {
  const image = new Image(); image.src = '/probe.png';
  const frame = document.createElement('iframe'); frame.src = '/probe-frame'; document.body.appendChild(frame);
  ready(callback => callback('!fixture'), () => {});
}};`

type pageTestTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	closed    atomic.Int32
}

func (transport *pageTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.roundTrip(request)
}

func (transport *pageTestTransport) CloseIdleConnections() { transport.closed.Add(1) }

func pageTestResponse(request *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func newTestGoPageRuntime(t *testing.T, transport http.RoundTripper) (*goWAARuntime, map[string]any) {
	t.Helper()
	directory := t.TempDir()
	options := camoufoxnative.Options{StorageStatePath: filepath.Join(directory, "storage-state.json"), Model: "fixture", Locale: "en-US", Timezone: "UTC", AuthUser: 3}
	cache, err := loadPageCache(options.StorageStatePath)
	if err != nil {
		t.Fatal(err)
	}
	session := &sessionCookies{}
	for _, name := range []string{"SAPISID", "__Secure-1PAPISID", "__Secure-3PAPISID"} {
		session.state.Cookies = append(session.state.Cookies, StateCookie{Name: name, Value: "offline-fixture", Domain: ".google.com", Path: "/", Expires: -1, Secure: true})
	}
	runtime, fingerprint, err := pageRuntime(options, &http.Client{Transport: transport}, session, cache)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, fingerprint
}

func pageFixtureRoundTrip(t *testing.T, source string, observe func(*http.Request)) func(*http.Request) (*http.Response, error) {
	t.Helper()
	challenge, err := json.Marshal([]any{[]any{"test", []string{source}, []string{}, waa.InterpreterHash([]byte(source)), "program", "fixture", nil, ""}})
	if err != nil {
		t.Fatal(err)
	}
	return func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if observe != nil {
			observe(request)
		}
		response := pageTestResponse(request, "[]")
		body := "[]"
		switch {
		case strings.HasSuffix(request.URL.Path, "/prompts/new_chat"):
			if request.URL.Query().Get("temporary") != "true" || request.URL.Query().Get("model") != "fixture" || !strings.HasPrefix(request.URL.Path, "/u/3/") {
				t.Errorf("cold-start URL = %s", request.URL)
			}
			body = pageFixtureDocument
		case request.URL.String() == waaCreateURL:
			body = string(challenge)
		case request.URL.String() == loggingContextURL:
			body = `["logging-fixture"]`
		case strings.HasPrefix(request.URL.String(), makerSuiteRPCURL):
			switch path.Base(request.URL.Path) {
			case "GetAiStudioBenefitTier":
				body = "[1]"
			case "ListModels":
				body = pageFixtureModels
			}
		case request.URL.Path == "/styles.css" || request.URL.Path == "/bundle.js" || request.URL.Path == "/icon.png":
			response.Header.Set("Cache-Control", "max-age=3600")
			response.Header.Set("ETag", `"fixture"`)
			body = "resource"
		case request.URL.String() == pageFixtureBundle:
			response.Header.Set("Cache-Control", "max-age=3600")
			body = scriptLoaderCall + "script);"
		case strings.HasSuffix(request.URL.Path, "/m="+starterModule):
			response.Header.Set("Cache-Control", "max-age=3600")
			body = `var fixtureModels=["models/fixture"];var series={id:"fixture",type:"text",Ek:a=>fixtureModels.includes(a.getName()),yl:[{title:"Fixture"}]};`
		case strings.HasSuffix(request.URL.Path, "/m="+starterDependency), request.URL.String() == feedbackScript:
			body = "script"
		case request.URL.String() == gapiLoaderScript:
			body = `h:"m;/_/scs/abc-static/_/js/k=fixture/d=1/rs=fixture/m=__features__",le:["fixture"]`
		case request.URL.Host == "apis.google.com" && strings.HasPrefix(request.URL.Path, "/_/scs/abc-static/_/js/"):
			body = "script"
		case strings.HasPrefix(request.URL.String(), cookieBarConfig):
			body = `{"content":{"mainText":"[This site] uses cookies","moreText":"Learn more","moreUrl":"https://policies.google.com/technologies/cookies"}}`
		case request.URL.String() == lockupImage || request.URL.String() == googleLogoImage || request.URL.String() == thinkingImage:
			response.Header.Set("Content-Type", "image/svg+xml")
			body = "image"
		case request.URL.Host == "play.google.com" && request.URL.Path == "/log":
			body = "[]"
		case request.URL.Path == "/probe.png":
			response.Header.Set("Content-Type", "image/png")
			body = "image"
		case request.URL.Path == "/probe-frame" || request.URL.Path == "/onboarding/auth_warmup":
			body = "frame"
		default:
			t.Errorf("unexpected offline page request: %s", request.URL)
			return nil, errors.New("unexpected fixture URL")
		}
		response.Body = io.NopCloser(strings.NewReader(body))
		return response, nil
	}
}

func TestGoPageOwnsTelemetryLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		arrived := make(chan struct{})
		var started sync.Once
		var requests atomic.Int32
		transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
			requests.Add(1)
			started.Do(func() { close(arrived) })
			<-request.Context().Done()
			return nil, request.Context().Err()
		}}
		runtime, _ := newTestGoPageRuntime(t, transport)
		runtime.elements = newPageElements(runtime.sendClearcut, runtime.after, runtime.stopTimer, "en-US", time.UTC, nil, "window", "en-US", "models/fixture")
		runtime.elements.mu.Lock()
		runtime.elements.pageEvent(veRun, time.Now(), nil)
		runtime.elements.mu.Unlock()
		runtime.elements.after(time.Now().Add(time.Hour), func() { t.Error("page element callback outlived page") })
		runtime.vitals = newPageVitals(runtime.sendClearcut, runtime.after, runtime.stopTimer, pageTelemetry{}, "en-US", time.UTC, time.Now(), map[string]any{"responseStart": 0, "loadEventEnd": 0}, "/prompts/new_chat")
		runtime.serviceStreamz = newStreamzReporter("en-US", time.UTC, func(body []byte) { runtime.sendClearcut(clearcutPlain, body) }, nil)
		runtime.serviceStreamz.record(streamzRecord{metric: "wrl", labels: []any{"Create"}, value: 1})
		runtime.sendClearcut(clearcutPlain, []byte(`[]`))
		<-arrived
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-runtime.serviceStreamz.done:
		default:
			t.Fatal("Close did not join the telemetry reporter")
		}
		count := requests.Load()
		time.Sleep(time.Hour)
		if requests.Load() != count || len(runtime.timers) != 0 || len(runtime.requests) != 0 || len(runtime.serviceStreamz.pending) != 0 {
			t.Fatal("closed page retained or dispatched telemetry work")
		}
	})
}

func TestStreamzCloseCancelsAndJoinsMetricRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		reporter := newStreamzReporter("en-US", time.UTC, func([]byte) { t.Error("closed reporter sent metrics") }, func(ctx context.Context) ([]waa.BotGuardMetric, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		<-started
		reporter.close()
		reporter.close()
		reporter.record(streamzRecord{metric: "ec"})
		if len(reporter.pending) != 0 {
			t.Fatal("closed reporter retained new records")
		}
	})
}

func TestGoPageColdStartCatalogAndReloadCache(t *testing.T) {
	var mu sync.Mutex
	counts := make(map[string]int)
	transport := &pageTestTransport{roundTrip: pageFixtureRoundTrip(t, pageFixtureInterpreter, func(request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		counts[request.URL.Path]++
		if request.Method == http.MethodPost && (counts["/styles.css"] == 0 || counts["/bundle.js"] == 0) {
			t.Error("page RPC started before document resources loaded")
		}
		if request.URL.Path == "/probe-frame" && (request.Header.Get("Sec-Fetch-Dest") != "iframe" || request.Header.Get("Sec-Fetch-Mode") != "navigate") {
			t.Error("frame loader lost its browser navigation headers")
		}
	})}
	runtime, fingerprint := newTestGoPageRuntime(t, transport)
	if err := runtime.open(t.Context(), fingerprint); err != nil {
		t.Fatal(err)
	}
	page := runtime.PageCatalog()
	if string(page.Logging) != `["logging-fixture"]` || string(page.Tier) != "[1]" || string(page.Models) != pageFixtureModels {
		t.Fatalf("cold-start catalog = %+v", page)
	}
	headers, err := runtime.ProtocolHeaders(t.Context())
	if err != nil || headers.Get("X-Goog-Api-Key") != "fixture-key" || headers.Get("X-Goog-Authuser") != "3" || headers.Get("X-Goog-Ext-519733851-Bin") == "" || headers.Get("X-Aistudio-Visit-Id") == "" {
		t.Fatalf("page protocol headers = %v, %v", headers, err)
	}
	nextRuntime, err := runtime.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := nextRuntime.(*goWAARuntime)
	defer next.Close()
	if next.vm == runtime.vm || next.session != runtime.session || next.pageCache != runtime.pageCache || next.client != runtime.client {
		t.Fatal("reload did not isolate the VM while retaining the account session/cache")
	}
	if next.headers.Get("X-Aistudio-Visit-Id") == headers.Get("X-Aistudio-Visit-Id") {
		t.Fatal("reload reused the old page visit ID")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if proof, _, err := next.RunProof(t.Context(), "digest", "hello", nil); err != nil || proof != "!fixture" {
		t.Fatalf("closing the old page damaged the replacement: %q, %v", proof, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/styles.css"] != 1 || counts["/bundle.js"] != 1 || counts["/probe-frame"] != 2 || counts["/probe.png"] != 2 {
		t.Fatalf("page resources ignored cache or host requests: %v", counts)
	}
}

func TestGoPageStartupRPCsDoNotBlockReadinessAndStopOnClose(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	fixture := pageFixtureRoundTrip(t, pageFixtureInterpreter, nil)
	transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case makerSuiteRPCURL + "GetUserPreferences":
			close(started)
			<-request.Context().Done()
			close(stopped)
			return nil, request.Context().Err()
		case loggingContextURL:
			select {
			case <-started:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		return fixture(request)
	}}
	runtime, fingerprint := newTestGoPageRuntime(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := runtime.open(ctx, fingerprint); err != nil {
		t.Fatalf("nonessential startup RPC blocked readiness: %v", err)
	}
	select {
	case <-stopped:
		t.Fatal("ready page canceled its background startup RPC")
	default:
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before the startup RPC released its resources")
	}
	if len(runtime.requests) != 0 || len(runtime.timers) != 0 || transport.closed.Load() != 1 {
		t.Fatal("closed page retained startup work")
	}
}

func TestGoPageFailedInitializationCancelsAndJoinsBackgroundHTTP(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	fixture := pageFixtureRoundTrip(t, pageFixtureInterpreter, nil)
	transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/onboarding/auth_warmup":
			close(started)
			<-request.Context().Done()
			close(stopped)
			return nil, request.Context().Err()
		case request.URL.String() == loggingContextURL:
			<-started
			return nil, errors.New("fixture logging failure")
		default:
			return fixture(request)
		}
	}}
	runtime, fingerprint := newTestGoPageRuntime(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := runtime.open(ctx, fingerprint); err == nil {
		t.Fatal("failed initialization was accepted")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("initialization failure left an auth iframe HTTP task running")
	}
	if runtime.ctx.Err() == nil || runtime.vm != nil || len(runtime.requests) != 0 || len(runtime.timers) != 0 || transport.closed.Load() != 1 {
		t.Fatal("failed page retained background work or idle connections")
	}
}

func TestGoPageCanceledInitializationInterruptsVM(t *testing.T) {
	transport := &pageTestTransport{roundTrip: pageFixtureRoundTrip(t, `for (;;) {}`, nil)}
	runtime, fingerprint := newTestGoPageRuntime(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := runtime.open(ctx, fingerprint); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled initialization = %v", err)
	}
	if !runtime.closed || runtime.vm != nil || runtime.interpreter != "" || len(runtime.requests) != 0 || len(runtime.timers) != 0 || transport.closed.Load() != 1 {
		t.Fatal("canceled initialization retained a VM, timer, or HTTP client resources")
	}
}

func TestGoPageCanRebuildCanceledProofVM(t *testing.T) {
	source := `var fixture = {a(program, ready) {
  ready(callback => {
    if (document.querySelector('textarea').value === 'busy') for (;;) {}
    callback('!fixture');
  }, () => {});
}};`
	transport := &pageTestTransport{roundTrip: pageFixtureRoundTrip(t, source, nil)}
	runtime, fingerprint := newTestGoPageRuntime(t, transport)
	if err := runtime.open(t.Context(), fingerprint); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := runtime.RunProof(ctx, "digest", "busy", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("busy proof = %v", err)
	}
	if runtime.vm != nil {
		t.Fatal("page retained an interrupted, unusable VM")
	}
	if proof, _, err := runtime.RunProof(t.Context(), "digest", "next", nil); err != nil || proof != "!fixture" {
		t.Fatalf("proof VM was not rebuilt: %q, %v", proof, err)
	}
}

func TestGoPageRefreshOutlivesCallerAndJoinsOnClose(t *testing.T) {
	for _, closePage := range []bool{false, true} {
		name := "complete"
		if closePage {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			var creates atomic.Int32
			fixture := pageFixtureRoundTrip(t, pageFixtureInterpreter, nil)
			transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == waaCreateURL && creates.Add(1) == 2 {
					close(started)
					select {
					case <-release:
					case <-request.Context().Done():
						return nil, request.Context().Err()
					}
				}
				return fixture(request)
			}}
			runtime, fingerprint := newTestGoPageRuntime(t, transport)
			if err := runtime.open(t.Context(), fingerprint); err != nil {
				t.Fatal(err)
			}
			previous := runtime.vm
			runtime.vmMu.Lock()
			runtime.vmExpires = time.Now().Add(-time.Second)
			runtime.vmMu.Unlock()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, _, err := runtime.RunProof(ctx, "digest", "canceled", nil); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("expired VM did not start a background refresh")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled proof waiter = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("proof cancellation waited for the shared VM refresh")
			}
			runtime.vmMu.Lock()
			refresh := runtime.refreshing
			reused := refresh != nil && runtime.refreshVM() == refresh
			runtime.vmMu.Unlock()
			if !reused {
				t.Fatal("caller cancellation lost or duplicated the shared VM refresh")
			}
			if closePage {
				go func() { done <- runtime.Close() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("page closure did not cancel and join the refresh")
				}
				select {
				case <-refresh.done:
				default:
					t.Fatal("page closed before its VM refresh completed")
				}
				if refresh.err == nil || runtime.vm != nil || runtime.interpreter != "" || len(runtime.requests) != 0 || len(runtime.timers) != 0 {
					t.Fatal("closed page retained refresh resources")
				}
				return
			}
			unblock()
			select {
			case <-refresh.done:
			case <-time.After(5 * time.Second):
				t.Fatal("background refresh did not complete")
			}
			if proof, _, err := runtime.RunProof(t.Context(), "digest", "next", nil); err != nil || proof != "!fixture" {
				t.Fatalf("refreshed proof = %q, %v", proof, err)
			}
			if runtime.vm == previous || creates.Load() != 2 {
				t.Fatal("refresh did not replace the VM exactly once")
			}
		})
	}
}

func TestGoPageCloseCancelsUnreadBodyAndScheduledRequests(t *testing.T) {
	body := &imageProbeBody{remaining: 1024}
	transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, Request: request}, nil
	}}
	runtime, _ := newTestGoPageRuntime(t, transport)
	response, err := runtime.SendProtected(t.Context(), "https://fixture.test/protected", http.Header{}, protectedBody{data: []byte("[]")})
	if err != nil {
		t.Fatal(err)
	}
	runtime.schedule(time.Hour, func(context.Context) { t.Error("closed page timer fired") })
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if !body.closed || len(runtime.requests) != 0 || len(runtime.timers) != 0 || runtime.schedule(0, func(context.Context) {}) != nil {
		t.Fatal("Close retained an unread body or allowed a new timer")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGoPageCompanionKeepsLeaseAndAdmissionUntilJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		account := &Account{ID: "fixture", Config: AccountConfig{Enabled: true}, State: AccountReady}
		pool := NewAccountPool([]*Account{account}, 1)
		lease, err := pool.AcquireFor(t.Context(), AccountSelection{AccountID: account.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
		runtime, _ := newTestGoPageRuntime(t, &pageTestTransport{roundTrip: func(*http.Request) (*http.Response, error) {
			t.Fatal("companion fixture must not make a network request")
			return nil, context.Canceled
		}})
		source := ContextWithAccountLease(ContextWithAccountScope(ContextWithAPIKey(ContextWithChannel(t.Context(), ChannelBuild), "admitted-key"), []string{account.ID}), lease)
		source = ContextWithAccount(source, account.ID)
		caller, cancel := context.WithCancel(source)
		defer cancel()
		started, completed := make(chan struct{}), make(chan struct{})
		runtime.scheduleContext(caller, 0, func(ctx context.Context) {
			if got, ok := AccountLeaseFromContext(ctx); !ok || got != lease || ChannelFromContext(ctx) != ChannelBuild || contextAccount(ctx) != account.ID {
				t.Error("companion lost its leased account or fixed channel")
			}
			if key, _ := ctx.Value(apiKeyContextKey{}).(string); key != "admitted-key" {
				t.Error("companion lost its admitted API key")
			}
			if scope, scoped := ctx.Value(accountScopeContextKey{}).([]string); !scoped || len(scope) != 1 || scope[0] != account.ID {
				t.Error("companion widened its admitted account scope")
			}
			close(started)
			<-ctx.Done()
			close(completed)
		})
		<-started
		cancel()
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-completed:
			t.Fatal("caller cancellation prematurely stopped a page companion")
		default:
		}
		if next, waitable, err := pool.TryAcquireFor(t.Context(), AccountSelection{AccountID: account.ID}); err != nil || next != nil || !waitable {
			t.Fatalf("companion released capacity before cleanup: %v, %t, %v", next, waitable, err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-completed:
		default:
			t.Fatal("page Close failed to cancel and join its companion")
		}
		next, _, err := pool.TryAcquireFor(t.Context(), AccountSelection{AccountID: account.ID})
		if err != nil || next == nil {
			t.Fatalf("joined companion retained capacity: %v, %v", next, err)
		}
		defer next.Release()
	})
}

func TestGoPageCanceledTimerReleasesRetainedLease(t *testing.T) {
	account := &Account{ID: "fixture", Config: AccountConfig{Enabled: true}, State: AccountReady}
	pool := NewAccountPool([]*Account{account}, 1)
	lease, err := pool.AcquireFor(t.Context(), AccountSelection{AccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	runtime, _ := newTestGoPageRuntime(t, &pageTestTransport{})
	timer := runtime.scheduleContext(ContextWithAccountLease(t.Context(), lease), time.Hour, func(context.Context) { t.Error("canceled companion timer fired") })
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	runtime.stopTimer(timer)
	runtime.stopTimer(timer)
	if lease.holds != 0 || !lease.released {
		t.Fatal("canceled timer leaked or released its lease more than once")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}
