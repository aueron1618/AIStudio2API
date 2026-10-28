package waa

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mag1cFall/goja"
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
	if vm.vm != nil || vm.snapshot != nil || vm.shutdown != nil || vm.loop.afterTask != nil {
		t.Fatal("closed runtime retained JS state")
	}
	select {
	case <-vm.loop.stopped:
	default:
		t.Fatal("event loop has not exited")
	}
	if err := vm.FillPrompt(t.Context(), "closed", false); err == nil {
		t.Fatal("closed runtime accepted prompt")
	}
}

func TestRuntimeStopInterruptsScript(t *testing.T) {
	vm := &Runtime{loop: newEventLoop(), vm: goja.New()}
	started := make(chan struct{})
	vm.loop.post(func() {
		close(started)
		_, _ = vm.vm.RunString(`for (;;) {}`)
	})
	<-started
	stopped := make(chan struct{})
	go func() { vm.stopVM(context.Canceled); close(stopped) }()
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
