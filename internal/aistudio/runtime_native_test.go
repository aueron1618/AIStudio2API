package aistudio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

type nativeRuntimeFixture struct {
	name      string
	closes    atomic.Int32
	failClose atomic.Int32
	next      workerRuntime
	reload    func(context.Context) (workerRuntime, error)
	run       bool
	digest    string
	prompt    string
	body      io.ReadCloser
}

func (runtime *nativeRuntimeFixture) Proof(_ context.Context, digest, prompt string) (string, error) {
	runtime.digest, runtime.prompt = digest, prompt
	return "!fixture", nil
}

func (runtime *nativeRuntimeFixture) RunProof(ctx context.Context, digest, prompt string) (string, error) {
	runtime.run = true
	return runtime.Proof(ctx, digest, prompt)
}

func (*nativeRuntimeFixture) ProtocolHeaders(context.Context) (http.Header, error) {
	return http.Header{"X-Goog-Api-Key": {"fixture"}}, nil
}

func (*nativeRuntimeFixture) PageCatalog() camoufoxnative.PageCatalog {
	return camoufoxnative.PageCatalog{Logging: []byte("[]"), Tier: []byte("[1]"), Models: []byte(pageFixtureModels)}
}

func (runtime *nativeRuntimeFixture) SendProtected(context.Context, string, http.Header, []byte) (*camoufoxnative.ProtectedResponse, error) {
	body := runtime.body
	if body == nil {
		body = io.NopCloser(strings.NewReader(runtime.name))
	}
	return &camoufoxnative.ProtectedResponse{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, nil
}

func (*nativeRuntimeFixture) StorageCookies(context.Context) ([]byte, error) {
	return []byte("[]"), nil
}

func (*nativeRuntimeFixture) MergeSetCookieHeaders(ctx context.Context, _ []string, _ string) error {
	return ctx.Err()
}

func TestCamoufoxCookieUpdatesRetainDeletionCommands(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	headers := []string{
		"sid=; Domain=.google.com; Path=/; Max-Age=0",
		"expired=; Domain=.google.com; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		"session=fresh; Domain=.google.com; Path=/; Secure",
		"persistent=fresh; Domain=.google.com; Path=/; Max-Age=3600; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
	}
	const source = "https://accounts.google.com/RotateBoundCookies"
	var updates StorageState
	if err := updates.mergeSetCookieHeaders(headers, source, now, true); err != nil {
		t.Fatal(err)
	}
	if len(updates.Cookies) != 4 || updates.Cookies[0].Name != "sid" || updates.Cookies[0].Expires != 0 ||
		updates.Cookies[1].Name != "expired" || updates.Cookies[1].Expires != 0 || updates.Cookies[2].Expires != -1 ||
		updates.Cookies[3].Expires != float64(now.Add(time.Hour).Unix()) {
		t.Fatalf("browser updates lost deletion or Max-Age precedence: %+v", updates.Cookies)
	}
	saved := StorageState{Cookies: []StateCookie{
		{Name: "sid", Value: "old", Domain: ".google.com", Path: "/", Expires: -1},
		{Name: "expired", Value: "old", Domain: ".google.com", Path: "/", Expires: -1},
		{Name: "unrelated", Value: "keep", Domain: ".google.com", Path: "/", Expires: -1},
	}}
	if err := saved.MergeSetCookieHeaders(headers, source, now); err != nil {
		t.Fatal(err)
	}
	if len(saved.Cookies) != 3 || saved.Cookies[0].Name != "unrelated" || saved.Cookies[0].Value != "keep" {
		t.Fatal("persisted state retained deleted cookies or dropped unrelated cookies")
	}
}

func (runtime *nativeRuntimeFixture) State() camoufoxnative.State {
	return camoufoxnative.State{PageURL: runtime.name}
}

func (runtime *nativeRuntimeFixture) Reload(ctx context.Context) (workerRuntime, error) {
	if runtime.reload != nil {
		return runtime.reload(ctx)
	}
	return runtime.next, nil
}

func (runtime *nativeRuntimeFixture) Close() error {
	runtime.closes.Add(1)
	if runtime.failClose.CompareAndSwap(1, 0) {
		return errors.New("fixture close failure")
	}
	return nil
}

func TestNativeReloadKeepsOldResponseUntilClosed(t *testing.T) {
	next := &nativeRuntimeFixture{name: "new"}
	previous := &nativeRuntimeFixture{name: "old", next: next}
	worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
	defer worker.Close()
	response, err := worker.SendProtected(t.Context(), ProtectedRequest{URL: "https://fixture.test/protected", Headers: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if previous.closes.Load() != 0 || worker.current() != next || worker.State().PageURL != "new" {
		t.Fatal("reload closed an in-flight old response or did not publish the new page")
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "old" {
		t.Fatalf("old response = %q, %v", data, err)
	}
	if previous.closes.Load() != 1 || next.closes.Load() != 0 {
		t.Fatal("EOF did not release only the retired runtime")
	}
	if err := response.Body.Close(); err != nil || previous.closes.Load() != 1 {
		t.Fatal("response Close released a retired runtime twice")
	}
}

func TestNativeCloseRetriesFailedRetiredRuntime(t *testing.T) {
	next := &nativeRuntimeFixture{name: "new"}
	previous := &nativeRuntimeFixture{name: "old", next: next}
	previous.failClose.Store(1)
	worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
	if err := worker.Reload(t.Context()); err == nil {
		t.Fatal("old runtime cleanup failure was hidden")
	}
	if len(worker.runtimeUses) != 1 || !worker.runtimeUses[previous].retired {
		t.Fatal("failed old runtime was forgotten")
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	if previous.closes.Load() != 2 || next.closes.Load() != 1 || len(worker.runtimeUses) != 0 || worker.State().Phase != WorkerClosed {
		t.Fatal("Close failed to account for both old and new runtimes")
	}
	if err := worker.Close(); err != nil || next.closes.Load() != 1 {
		t.Fatal("successful worker Close is not idempotent")
	}
}

func TestNativeReloadCannotPublishAfterClose(t *testing.T) {
	ready, proceed := make(chan struct{}), make(chan struct{})
	next := &nativeRuntimeFixture{name: "new"}
	previous := &nativeRuntimeFixture{name: "old", reload: func(context.Context) (workerRuntime, error) {
		close(ready)
		<-proceed
		return next, nil
	}}
	worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
	done := make(chan error, 1)
	go func() { done <- worker.Reload(t.Context()) }()
	<-ready
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-done; err == nil {
		t.Fatal("a replacement was published after Close")
	}
	if worker.current() != previous || previous.closes.Load() != 1 || next.closes.Load() != 1 || worker.State().Phase != WorkerClosed {
		t.Fatal("late reload leaked a replacement or revived a closed worker")
	}
}

func TestNativeCanceledReplacementCloseFailureIsTracked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	next := &nativeRuntimeFixture{name: "new"}
	next.failClose.Store(1)
	previous := &nativeRuntimeFixture{name: "old", reload: func(context.Context) (workerRuntime, error) {
		cancel()
		return next, nil
	}}
	worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
	if err := worker.Reload(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reload = %v", err)
	}
	if worker.current() != previous || worker.runtimeUses[next] == nil || !worker.runtimeUses[next].retired {
		t.Fatal("failed replacement cleanup was lost")
	}
	if err := worker.Close(); err != nil || next.closes.Load() != 2 || previous.closes.Load() != 1 {
		t.Fatal("Close did not retry the canceled replacement")
	}
}

func TestNativePrepareUsesRunOnlyForPageGenerate(t *testing.T) {
	for _, run := range []bool{false, true} {
		runtime := &nativeRuntimeFixture{}
		worker := &NativeWorker{runtime: runtime}
		request := ProtectedRequest{URL: makerSuiteRPCURL + "GenerateContent", Body: []byte(`[null,null,null,null,null]`), ProofField: 5, Prompt: "binding"}
		if run {
			request.PagePrompt = "typed"
		}
		prepared, err := worker.Prepare(t.Context(), request)
		digest := sha256.Sum256([]byte("binding"))
		if err != nil || runtime.run != run || runtime.digest != fmt.Sprintf("%x", digest) || runtime.prompt != request.PagePrompt || !strings.Contains(string(prepared.Body), `"!fixture"`) {
			t.Fatalf("Prepare run=%v: %+v, %v", run, prepared, err)
		}
	}
}

type nativeGenerateFixture struct {
	*nativeRuntimeFixture
	turn camoufoxnative.GenerateTurn
}

func (runtime *nativeGenerateFixture) Generate(ctx context.Context, turn camoufoxnative.GenerateTurn) (*camoufoxnative.ProtectedResponse, error) {
	runtime.turn = turn
	return runtime.SendProtected(ctx, "", nil, nil)
}

func TestNativeGenerateUsesPageRunWithoutPrecomputedProof(t *testing.T) {
	runtime := &nativeGenerateFixture{nativeRuntimeFixture: &nativeRuntimeFixture{name: "generated"}}
	worker := &NativeWorker{runtime: runtime}
	body, err := json.Marshal([]any{"model", []any{[]any{[]any{[]any{nil, "hi"}}, "user"}}, nil, nil, nil})
	if err != nil {
		t.Fatal(err)
	}
	request := ProtectedRequest{URL: makerSuiteRPCURL + "GenerateContent", Body: body, ProofField: 5, Prompt: "binding", PagePrompt: "hi"}
	prepared, err := worker.Prepare(t.Context(), request)
	if err != nil || string(prepared.Body) != string(body) || runtime.digest != "" {
		t.Fatal("Camoufox Generate was given a precomputed proof instead of a real Run")
	}
	response, err := worker.SendProtected(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	digest := sha256.Sum256([]byte("binding"))
	if runtime.turn.Digest != fmt.Sprintf("%x", digest) || string(runtime.turn.Body[runtime.turn.ProofStart:runtime.turn.ProofEnd]) != "null" {
		t.Fatalf("GenerateTurn lost binding or proof span: %+v", runtime.turn)
	}
}

func TestNativeGenerateReusesContinuationProof(t *testing.T) {
	runtime := &nativeGenerateFixture{nativeRuntimeFixture: &nativeRuntimeFixture{name: "continued"}}
	worker := &NativeWorker{runtime: runtime}
	defer worker.Close()
	body, err := json.Marshal([]any{"model", []any{[]any{[]any{[]any{nil, "hi"}}, "user"}}, nil, nil, nil})
	if err != nil {
		t.Fatal(err)
	}
	request := ProtectedRequest{URL: makerSuiteRPCURL + "GenerateContent", Body: body, ProofField: 5, Prompt: "binding", PagePrompt: "hi", Proof: "!first-round"}
	prepared, err := worker.Prepare(t.Context(), request)
	if err != nil || prepared.Proof != request.Proof || !strings.Contains(string(prepared.Body), `"!first-round"`) || runtime.digest != "" || runtime.run {
		t.Fatalf("continuation generated a new proof: prepared=%+v err=%v", prepared, err)
	}
	request.Body, request.Headers, request.Proof = prepared.Body, prepared.Headers, prepared.Proof
	response, err := worker.SendProtected(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Proof != request.Proof || len(runtime.turn.Body) != 0 {
		t.Errorf("continuation reran the page or lost its proof: response=%+v turn=%+v", response, runtime.turn)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if use := worker.runtimeUses[runtime]; use == nil || use.requests != 0 || len(use.responses) != 0 {
		t.Fatal("proof reuse bypassed native response lifetime tracking")
	}
}

type nativeCanceledBody struct {
	err       error
	closing   chan struct{}
	allow     chan struct{}
	closeCall atomic.Int32
}

func (body *nativeCanceledBody) Read([]byte) (int, error) { return 0, body.err }

func (body *nativeCanceledBody) Close() error {
	body.closeCall.Add(1)
	close(body.closing)
	<-body.allow
	return nil
}

func TestNativeCanceledReadIsPromptAndCloseJoinsCleanup(t *testing.T) {
	for _, readErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(readErr.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				body := &nativeCanceledBody{err: readErr, closing: make(chan struct{}), allow: make(chan struct{})}
				next := &nativeRuntimeFixture{name: "new"}
				opened := make(chan struct{}, 1)
				previous := &nativeRuntimeFixture{name: "old", body: body, reload: func(context.Context) (workerRuntime, error) {
					opened <- struct{}{}
					return next, nil
				}}
				worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
				defer worker.Close()
				response, err := worker.SendProtected(t.Context(), ProtectedRequest{URL: "https://fixture.test/protected", Headers: http.Header{}})
				if err != nil {
					t.Fatal(err)
				}
				read := make(chan error, 1)
				go func() { _, err := response.Body.Read(make([]byte, 1)); read <- err }()
				<-body.closing
				synctest.Wait()
				if len(read) == 0 {
					close(body.allow)
					<-read
					t.Fatal("native canceled Read waited for page cleanup")
				}
				if err := <-read; !errors.Is(err, readErr) {
					t.Fatalf("Read error = %v", err)
				}
				reloaded := make(chan error, 1)
				go func() { reloaded <- worker.Reload(t.Context()) }()
				closed := make(chan error, 2)
				for range 2 {
					go func() { closed <- response.Body.Close() }()
				}
				synctest.Wait()
				if len(closed) != 0 || previous.closes.Load() != 0 || worker.runtimeUses[previous].requests != 1 {
					t.Error("Close released the old runtime before its source cleanup completed")
				}
				if len(reloaded) != 0 || len(opened) != 0 {
					t.Error("Reload treated canceled response cleanup as a live response and opened early")
				}
				close(body.allow)
				for range 2 {
					if err := <-closed; err != nil {
						t.Fatal(err)
					}
				}
				if err := <-reloaded; err != nil {
					t.Fatal(err)
				}
				if body.closeCall.Load() != 1 || previous.closes.Load() != 1 || next.closes.Load() != 0 || worker.runtimeUses[previous] != nil {
					t.Fatal("cleanup was duplicated, leaked, or released the replacement runtime")
				}
			})
		})
	}
}

func TestNativeReloadCancellationKeepsClosingResponseOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := &nativeCanceledBody{err: context.Canceled, closing: make(chan struct{}), allow: make(chan struct{})}
		opened := make(chan struct{}, 1)
		runtime := &nativeRuntimeFixture{body: body, reload: func(context.Context) (workerRuntime, error) {
			opened <- struct{}{}
			return nil, errors.New("unexpected reload")
		}}
		worker := &NativeWorker{runtime: runtime, state: WorkerState{Phase: WorkerReady}}
		defer worker.Close()
		response, err := worker.SendProtected(t.Context(), ProtectedRequest{URL: "https://fixture.test/protected", Headers: http.Header{}})
		if err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- response.Body.Close() }()
		<-body.closing
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reloaded := make(chan error, 1)
		go func() { reloaded <- worker.Reload(ctx) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-reloaded:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Reload = %v", err)
			}
		default:
			t.Error("Reload cancellation waited for response cleanup")
		}
		if len(opened) != 0 || len(closed) != 0 || worker.runtimeUses[runtime].requests != 1 {
			t.Error("canceled Reload opened a page or released pending cleanup")
		}
		close(body.allow)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if worker.runtimeUses[runtime].requests != 0 || len(worker.runtimeUses[runtime].responses) != 0 {
			t.Fatal("completed cleanup retained its response reference")
		}
	})
}

func TestNativeNextRequestJoinsCanceledResponse(t *testing.T) {
	for _, readCanceled := range []bool{false, true} {
		t.Run(fmt.Sprint(readCanceled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				body := &nativeCanceledBody{err: context.Canceled, closing: make(chan struct{}), allow: make(chan struct{})}
				runtime := &nativeRuntimeFixture{body: body}
				worker := &NativeWorker{runtime: runtime, state: WorkerState{Phase: WorkerReady}}
				defer worker.Close()
				request := ProtectedRequest{URL: "https://fixture.test/protected", Headers: http.Header{}}
				response, err := worker.SendProtected(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				runtime.body = io.NopCloser(strings.NewReader("next"))
				if readCanceled {
					if _, err := response.Body.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else {
					cancel()
				}
				var next *RPCResponse
				var nextErr error
				sent := make(chan struct{})
				go func() {
					next, nextErr = worker.SendProtected(t.Context(), request)
					close(sent)
				}()
				synctest.Wait()
				select {
				case <-sent:
					t.Error("a new request reused runtime capacity before canceled cleanup completed")
				default:
				}
				closed := make(chan error, 1)
				go func() { closed <- response.Body.Close() }()
				<-body.closing
				close(body.allow)
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
				<-sent
				if nextErr != nil || next == nil {
					t.Fatalf("next request did not resume after cleanup: %v", nextErr)
				}
				if err := next.Body.Close(); err != nil || body.closeCall.Load() != 1 {
					t.Fatalf("cleanup was not joined exactly once: %v", err)
				}
			})
		})
	}
}

func TestNativeReloadJoinsCanceledResponseBeforeCloseStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := &nativeCanceledBody{err: context.Canceled, closing: make(chan struct{}), allow: make(chan struct{})}
		next := &nativeRuntimeFixture{name: "new"}
		opened := make(chan struct{}, 1)
		previous := &nativeRuntimeFixture{body: body, reload: func(context.Context) (workerRuntime, error) {
			opened <- struct{}{}
			return next, nil
		}}
		worker := &NativeWorker{runtime: previous, state: WorkerState{Phase: WorkerReady}}
		defer worker.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		response, err := worker.SendProtected(ctx, ProtectedRequest{URL: "https://fixture.test/protected", Headers: http.Header{}})
		if err != nil {
			t.Fatal(err)
		}
		// The source can finish before its AfterFunc Close callback is scheduled.
		cancel()
		reloaded := make(chan error, 1)
		go func() { reloaded <- worker.Reload(t.Context()) }()
		synctest.Wait()
		if len(opened) != 0 || len(reloaded) != 0 {
			t.Error("Reload opened before the canceled response began cleanup")
		}
		select {
		case <-body.closing:
		default:
			t.Error("Reload did not start the canceled response's shared cleanup")
		}
		closed := make(chan error, 1)
		go func() { closed <- response.Body.Close() }()
		synctest.Wait()
		close(body.allow)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if err := <-reloaded; err != nil {
			t.Fatal(err)
		}
		if body.closeCall.Load() != 1 || previous.closes.Load() != 1 || worker.runtimeUses[previous] != nil {
			t.Fatal("Reload and the late Close did not share cleanup ownership")
		}
	})
}
