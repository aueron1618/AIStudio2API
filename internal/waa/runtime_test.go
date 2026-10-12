package waa

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
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
	if vm.vm != nil || vm.snapshot != nil || vm.shutdown != nil {
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
