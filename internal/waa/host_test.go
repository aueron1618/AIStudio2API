package waa

import (
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

func newTestHostState() (*goja.Runtime, *hostState) {
	vm := goja.New()
	state := &hostState{
		registry: newHostRegistry(),
		profile:  Profile{Locale: "en-US", TimeZone: "UTC", PageURL: "https://aistudio.google.com/prompts/new_chat"},
		location: time.UTC,
		schedule: func(time.Duration, func()) {},
	}
	return vm, state
}

func testRealmHost(t testing.TB) (*goja.Runtime, *hostState) {
	t.Helper()
	vm, state := newTestHostState()
	api, err := state.installRealmHost(vm, "top", nil)
	if err != nil {
		t.Fatal(err)
	}
	state.topAPI = api
	return vm, state
}

func TestRealmHostIsolation(t *testing.T) {
	first, state := testRealmHost(t)
	frame, err := newIframeWindow(first, state)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Get("Object") == first.Get("Object") {
		t.Fatal("iframe constructors must belong to their own realm")
	}
	if _, err := first.RunString(`HTMLTextAreaElement.prototype.testMarker = 42`); err != nil {
		t.Fatal(err)
	}
	second, _ := testRealmHost(t)
	value, err := second.RunString(`HTMLTextAreaElement.prototype.testMarker`)
	if err != nil || !goja.IsUndefined(value) {
		t.Fatalf("host state leaked across VMs: %v, %v", value, err)
	}
}

// BenchmarkRealmHost measures repeated account/iframe host creation without network I/O.
func BenchmarkRealmHost(b *testing.B) {
	testRealmHost(b) // Warm process-wide immutable data before measuring.
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		vm, state := testRealmHost(b)
		if _, err := newIframeWindow(vm, state); err != nil {
			b.Fatal(err)
		}
	}
}
