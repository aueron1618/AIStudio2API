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
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

const pageFixtureModels = `[[["models/fixture",null,"v1","Fixture",null,1024,256,["generateContent"]]]]`

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
			body = `<html><head><script>var flags = {"WIu0Nc":"fixture-key"};</script><link rel="stylesheet" href="/styles.css"><link rel="preload" as="script" href="/bundle.js"><script src="/bundle.js"></script><link rel="icon" href="/icon.png" sizes="16x16"></head><body></body></html>`
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
	if proof, err := next.RunProof(t.Context(), "digest", "hello"); err != nil || proof != "!fixture" {
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
	if _, err := runtime.RunProof(ctx, "digest", "busy"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("busy proof = %v", err)
	}
	if runtime.vm != nil {
		t.Fatal("page retained an interrupted, unusable VM")
	}
	if proof, err := runtime.RunProof(t.Context(), "digest", "next"); err != nil || proof != "!fixture" {
		t.Fatalf("proof VM was not rebuilt: %q, %v", proof, err)
	}
}

func TestGoPageCloseCancelsUnreadBodyAndScheduledRequests(t *testing.T) {
	body := &imageProbeBody{remaining: 1024}
	transport := &pageTestTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, Request: request}, nil
	}}
	runtime, _ := newTestGoPageRuntime(t, transport)
	response, err := runtime.SendProtected(t.Context(), "https://fixture.test/protected", http.Header{}, []byte("[]"))
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
