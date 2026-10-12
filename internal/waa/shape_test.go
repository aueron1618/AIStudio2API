package waa

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

func TestShapeDataMatchesJSON(t *testing.T) {
	vm := goja.New()
	shape, err := firefoxShape(vm)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("shape", shape); err != nil {
		t.Fatal(err)
	}
	source, err := firefoxShapeJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("source", source); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString(`JSON.stringify(shape) === JSON.stringify(JSON.parse(source))`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("shape data/order changed: %v, %v", value, err)
	}
	value, err = vm.RunString(`(() => {
  const name = shape.interfaces[0].n;
  if (Reflect.set(shape.interfaces[0], 'n', 'changed')) return false;
  if (Reflect.set(shape.interfaces, 'length', 0)) return false;
  if (Reflect.deleteProperty(shape, 'interfaces')) return false;
  return shape.interfaces[0].n === name && Array.isArray(shape.interfaces);
})()`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("shape must be read-only: %v, %v", value, err)
	}
}

func TestShapeArraysProducePrivateResults(t *testing.T) {
	value, err := readShapeValue(json.NewDecoder(strings.NewReader(`{"names":["a","b"],"nil":null}`)))
	if err != nil {
		t.Fatal(err)
	}
	data := &shapeData{value: value}
	for range 2 {
		vm := goja.New()
		if err := vm.Set("shape", wrapShapeValue(vm, data, value)); err != nil {
			t.Fatal(err)
		}
		result, err := vm.RunString(`(() => {
  const names = shape.names.map(x => x);
  names[0] = 'private';
  return shape.names.join(',') === 'a,b' && shape.nil === null;
})()`)
		if err != nil || !result.ToBoolean() {
			t.Fatalf("array result shared: %v, %v", result, err)
		}
	}
}

// legacyRealmHost retains the previous JSON.parse path solely for comparative benchmarks.
func legacyRealmHost(t testing.TB) (*goja.Runtime, *hostState) {
	t.Helper()
	vm, state := newTestHostState()
	source, err := firefoxShapeJSON()
	if err != nil {
		t.Fatal(err)
	}
	parse, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("parse"))
	state.shape, err = parse(goja.Undefined(), vm.ToValue(source))
	if err != nil {
		t.Fatal(err)
	}
	state.order, err = firefoxOrderShape()
	if err != nil {
		t.Fatal(err)
	}
	weakMap, _ := goja.AssertConstructor(vm.Get("WeakMap"))
	state.brands, err = weakMap(nil)
	if err != nil {
		t.Fatal(err)
	}
	state.storage = vm.NewObject()
	api, err := state.installRealmHost(vm, "top", nil)
	if err != nil {
		t.Fatal(err)
	}
	state.topAPI = api
	return vm, state
}

func TestRealmHostLiveHeap(t *testing.T) {
	for _, test := range []struct {
		name   string
		create func(testing.TB) (*goja.Runtime, *hostState)
	}{{"legacy-json", legacyRealmHost}, {"compact-shape", testRealmHost}} {
		t.Run(test.name, func(t *testing.T) {
			// Keep a warmed instance alive so the delta excludes shared immutable data.
			warm, warmState := test.create(t)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			vm, state := test.create(t)
			frame, err := newIframeWindow(vm, state)
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			t.Logf("additional top+iframe live heap: %.2f MiB", float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
			runtime.KeepAlive(frame)
			runtime.KeepAlive(vm)
			runtime.KeepAlive(state)
			runtime.KeepAlive(warm)
			runtime.KeepAlive(warmState)
		})
	}
}

func BenchmarkRealmHostShape(b *testing.B) {
	for _, test := range []struct {
		name   string
		create func(testing.TB) (*goja.Runtime, *hostState)
	}{{"legacy-json", legacyRealmHost}, {"compact-shape", testRealmHost}} {
		b.Run(test.name, func(b *testing.B) {
			warm, warmState := test.create(b)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				vm, state := test.create(b)
				if _, err := newIframeWindow(vm, state); err != nil {
					b.Fatal(err)
				}
			}
			runtime.KeepAlive(warm)
			runtime.KeepAlive(warmState)
		})
	}
}
