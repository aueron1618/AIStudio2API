package waa

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

func TestObjectStringsLiveObject(t *testing.T) {
	vm := goja.New()
	table := newObjectStrings()
	object := vm.NewObject()
	table.set(object, "first")
	table.set(object, "updated")
	runtime.GC()
	if value, ok := table.get(object); !ok || value != "updated" {
		t.Fatalf("live marker lost: %q, %v", value, ok)
	}
	if _, ok := table.get(vm.NewObject()); ok {
		t.Fatal("marker leaked to another object")
	}
	if len(object.Keys()) != 0 || len(object.Symbols()) != 0 {
		t.Fatal("host markers must not be visible to JavaScript")
	}
	runtime.KeepAlive(object)
}

func addTransientMarker(table *objectStrings) weak.Pointer[goja.Object] {
	object := goja.New().NewObject()
	table.set(object, "temporary source")
	return weak.Make(object)
}

func TestObjectStringsReleasesUnreachableObjects(t *testing.T) {
	table := newObjectStrings()
	object := addTransientMarker(table)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		table.mu.Lock()
		remaining := len(table.values)
		table.mu.Unlock()
		if object.Value() == nil && remaining == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unreachable object or marker was retained")
}

func TestTrustedScriptMarkers(t *testing.T) {
	vm := goja.New()
	registry := newHostRegistry()
	object := vm.NewObject()
	registry.trusted.set(object, "1 + 2")
	transform := registry.evalTransformer(vm, false)
	if got := transform(object).String(); got != "1 + 2" {
		t.Fatalf("trusted script = %q", got)
	}
	unmarked := vm.NewObject()
	if transform(unmarked) != unmarked {
		t.Fatal("unmarked object must not become source code")
	}
}
