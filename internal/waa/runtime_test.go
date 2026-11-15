package waa

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeCloseReleasesVM(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge:   Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) { ready(callback => callback('!test'), () => {}); }};`,
		Profile:     Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := vm.Proof(t.Context(), "digest", "hello")
	if err != nil || proof != "!test" {
		t.Fatalf("proof = %q, %v", proof, err)
	}
	vm.Close()
	vm.Close()
	if vm.agent != nil || vm.snapshot != 0 || vm.shutdown != 0 || vm.mouse != 0 || vm.key != 0 || vm.loop.afterTask != nil || vm.results != nil || vm.metrics != nil {
		t.Fatal("closed runtime retained JS state")
	}
	vm.resetIdle()
	if vm.idle != nil || len(vm.loop.timers) != 0 {
		t.Fatal("closed runtime retained or rearmed timers")
	}
	select {
	case <-vm.loop.exited:
	default:
		t.Fatal("event loop has not exited")
	}
	if err := vm.fillPrompt(t.Context(), "closed"); err == nil {
		t.Fatal("closed runtime accepted prompt")
	}
}

func TestRuntimeStopInterruptsScript(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge:   Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) { ready(callback => callback('!test'), () => {}); }};`,
		Profile:     Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	vm.loop.post(func() {
		close(started)
		_, _ = vm.agent.evaluate(vm.agent.top.global, `for (;;) {}`, "test.js")
	})
	<-started
	stopped := make(chan struct{})
	go func() { vm.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("script was not interrupted")
	}
}

func TestRuntimeCanceledInitialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	vm, err := NewRuntime(ctx, Options{
		Challenge:   Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `for (;;) {}`,
		Profile:     Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if vm != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled init = %v, %v", vm, err)
	}
}

func TestRuntimeReadyCallbackDoesNotHideCanceledInitialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	vm, err := NewRuntime(ctx, Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  ready(callback => callback('!test'), () => {});
  for (;;) {}
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if vm != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ready-but-busy init = %v, %v", vm, err)
	}
}

func TestRuntimeCanceledProofStopsBusyScript(t *testing.T) {
	started := make(chan struct{})
	vm, err := NewRuntime(t.Context(), Options{
		Challenge:   Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) { ready(() => { new Image().src = '/snapshot'; for (;;) {} }, () => {}); }};`,
		Profile:     Profile{Locale: "en-US", TimeZone: "UTC", PageURL: "https://aistudio.google.com/"},
		LoadImage:   func(string) bool { close(started); return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := vm.RunProof(ctx, "digest", "hello"); finished <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("RunProof did not enter the snapshot script")
	}
	if err := <-finished; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled proof = %v", err)
	}
	select {
	case <-vm.loop.exited:
	default:
		t.Fatal("cancellation only stopped the Go waiter, not the JS thread")
	}
	if vm.agent != nil || vm.results != nil || vm.idle != nil {
		t.Fatal("canceled proof retained runtime roots")
	}
}

func TestRuntimeRunProofDispatchesInputBeforeRun(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  const box = document.createElement('ms-prompt-box');
  const textarea = document.createElement('textarea');
  box.appendChild(textarea); document.body.appendChild(box);
  const run = document.createElement('ms-run-button');
  const button = document.createElement('button');
  button.appendChild(document.createElement('span'));
  run.appendChild(button); document.body.appendChild(run);
  const events = [];
  textarea.addEventListener('input', () => events.push('input:' + textarea.value));
  textarea.addEventListener('change', () => events.push('change'));
  button.addEventListener('click', () => events.push('run'));
  ready(callback => callback('!' + events.join('|') + ':' + textarea.value), () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	proof, err := vm.RunProof(t.Context(), "digest", "a\r\nb")
	if err != nil || proof != "!input:a\nb|change|run:a\nb" {
		t.Fatalf("RunProof = %q, %v", proof, err)
	}
	if vm.pointer.Clicked().IsZero() {
		t.Fatal("RunProof did not record the trusted click time")
	}
	proof, err = vm.RefreshSnapshot(t.Context())
	if err != nil || proof != "!input:a\nb|change|run:" {
		t.Fatalf("RunProof retained input or dispatched cleanup events: %q, %v", proof, err)
	}
}

func TestRuntimeProofClearsPrompt(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  ready((callback, options) => callback('!' + document.querySelector('textarea').value), () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC", TrustedTypes: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	proof, err := vm.Proof(t.Context(), "digest", "line1\r\nline2\rline3")
	if err != nil || proof != "!line1\nline2\nline3" {
		t.Fatalf("normalized proof = %q, %v", proof, err)
	}
	proof, err = vm.RefreshSnapshot(t.Context())
	if err != nil || proof != "!" {
		t.Fatalf("prompt was retained after proof: %q, %v", proof, err)
	}
}

func TestRuntimeSubmitProofClearsNormalizedResult(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  const events = [];
  addEventListener('input', event => events.push(event.isTrusted));
  addEventListener('click', event => events.push(event.isTrusted));
  ready(callback => callback('!' + document.querySelector('ms-function-call-chunk').querySelector('input').value + ':' + events.join(',')), () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	proof, err := vm.SubmitProof(t.Context(), "digest", "a\r\nb\rc", 3)
	if err != nil || proof != "!abc:true,true,true" {
		t.Fatalf("SubmitProof = %q, %v", proof, err)
	}
	proof, err = vm.RefreshSnapshot(t.Context())
	if err != nil || proof != "!:true,true,true" {
		t.Fatalf("function result was retained or cleanup dispatched input: %q, %v", proof, err)
	}
}

func TestRuntimeSettleWaitsForNestedResources(t *testing.T) {
	first, second := make(chan struct{}), make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(first) })
	releaseSecond := sync.OnceFunc(func() { close(second) })
	defer releaseFirst()
	defer releaseSecond()
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  const image = new Image();
  image.onload = () => { const nested = new Image(); nested.src = '/second.png'; };
  image.src = '/first.png';
  ready(callback => callback('!test'), () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC", PageURL: "https://aistudio.google.com/"},
		LoadImage: func(address string) bool {
			if strings.HasSuffix(address, "/first.png") {
				<-first
			} else if strings.HasSuffix(address, "/second.png") {
				close(secondStarted)
				<-second
			}
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	done := make(chan error, 1)
	go func() { done <- vm.Settle(t.Context()) }()
	releaseFirst()
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("resource callback did not start its nested request")
	}
	select {
	case err := <-done:
		t.Fatalf("Settle returned before the nested request completed: %v", err)
	default:
	}
	releaseSecond()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Settle did not finish after all resource callbacks")
	}
	vm.Close()
	if err := vm.Settle(t.Context()); err == nil {
		t.Fatal("Settle accepted a closed runtime")
	}
}

func TestRuntimeRejectionNotificationOrder(t *testing.T) {
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: "test", GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  const events = [];
  addEventListener('unhandledrejection', event => { events.push('unhandled:' + event.reason); event.preventDefault(); });
  addEventListener('rejectionhandled', event => events.push('handled:' + event.reason));
  const rejected = Promise.reject('test');
  ready(callback => { callback('!' + events.join(',')); rejected.catch(() => {}); }, () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	for _, want := range []string{"!unhandled:test", "!unhandled:test,handled:test"} {
		proof, err := vm.Proof(t.Context(), "digest", "hello")
		if err != nil || proof != want {
			t.Fatalf("rejection notification = %q, %v; want %q", proof, err, want)
		}
	}
}

func TestRuntimeInputCancellationStopsBusyScript(t *testing.T) {
	for _, operation := range []string{"keyboard", "pointer", "activation"} {
		t.Run(operation, func(t *testing.T) {
			vm := testRealmHost(t, "true")
			defer vm.Close()
			started := make(chan struct{})
			vm.loop.post(func() {
				close(started)
				_, _ = vm.agent.evaluate(vm.agent.top.global, `for (;;) {}`, "busy.js")
			})
			<-started
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			var err error
			switch operation {
			case "keyboard":
				_, err = vm.keyInput(ctx, "prompt", "text", []string{"input"})
			case "pointer":
				err = vm.Replaced(ctx)
			case "activation":
				err = vm.Activate(ctx)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("canceled %s = %v", operation, err)
			}
			select {
			case <-vm.loop.exited:
			default:
				t.Fatal("input cancellation did not join the JS thread")
			}
			if vm.agent != nil || vm.mouse != 0 || vm.key != 0 || vm.metrics != nil || vm.idle != nil {
				t.Fatal("canceled input retained VM state")
			}
		})
	}
}

func TestRuntimeMetricsAreDrained(t *testing.T) {
	vm := testRealmHost(t, "true")
	defer vm.Close()
	initial, err := vm.TakeMetrics(t.Context())
	if err != nil || len(initial) != 2 || initial[0].Metric != "ec" || initial[0].Label != "t" || initial[1].Metric != "el" || initial[1].Label != "t" {
		t.Fatalf("initial metrics = %+v, %v", initial, err)
	}
	proof, err := vm.Proof(t.Context(), "digest", "text")
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := vm.TakeMetrics(t.Context())
	if err != nil || len(metrics) != 3 || metrics[0] != (BotGuardMetric{Metric: "ec", Label: "n", Value: 1}) || metrics[1].Metric != "el" || metrics[1].Label != "n" || metrics[1].Value < 0 || metrics[2] != (BotGuardMetric{Metric: "frs", Value: float64(len(proof))}) {
		t.Fatalf("snapshot metrics = %+v, %v", metrics, err)
	}
	if taken, err := vm.TakeMetrics(t.Context()); err != nil || len(taken) != 0 {
		t.Fatalf("metrics were retained after draining: %+v, %v", taken, err)
	}
	vm.Close()
	if _, err := vm.TakeMetrics(t.Context()); err == nil {
		t.Fatal("closed runtime accepted a metrics request")
	}
}
