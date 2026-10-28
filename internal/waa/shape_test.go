package waa

import (
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/Mag1cFall/goja"
)

func TestShapeDataMatchesJSON(t *testing.T) {
	vm := goja.New()
	const closureName = "closure_lm_42"
	shape, err := firefoxShape(vm, closureName)
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
	if err := vm.Set("source", replaceClosureListenerMap(source, closureName)); err != nil {
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
		view := &shapeView{vm: vm, data: data}
		if err := vm.Set("shape", view.wrap(value)); err != nil {
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

func TestShapeViewsKeepClosureNamesPrivate(t *testing.T) {
	const source = `{"before":0,"closure_lm_123":{"name":"prefix closure_lm_123 suffix"},"names":["closure_lm_123"],"after":1}`
	value, err := readShapeValue(json.NewDecoder(strings.NewReader(source)))
	if err != nil {
		t.Fatal(err)
	}
	data := &shapeData{value: value}
	for _, name := range []string{"closure_lm_42", "closure_lm_99", ""} {
		vm := goja.New()
		view := &shapeView{vm: vm, data: data, closureName: name}
		if err := vm.Set("shape", view.wrap(value)); err != nil {
			t.Fatal(err)
		}
		got, err := vm.RunString(`JSON.stringify(shape)`)
		want := replaceClosureListenerMap(source, name)
		if err != nil || got.String() != want {
			t.Fatalf("view %q changed data/order: %v, %v; want %s", name, got, err, want)
		}
		if name != "" {
			got, err = vm.RunString(`'closure_lm_123' in shape`)
			if err != nil || got.ToBoolean() {
				t.Fatalf("captured listener key leaked into view %q: %v, %v", name, got, err)
			}
		}
	}
}

func TestShapeCacheSharesOnlyLiveData(t *testing.T) {
	pointer := func() weak.Pointer[shapeData] {
		first, err := firefoxShape(goja.New(), "closure_lm_42")
		if err != nil {
			t.Fatal(err)
		}
		second, err := firefoxShape(goja.New(), "closure_lm_99")
		if err != nil {
			t.Fatal(err)
		}
		firstView := first.Export().(*shapeObject).view
		secondView := second.Export().(*shapeObject).view
		if firstView == secondView || firstView.vm == secondView.vm || firstView.data != secondView.data {
			t.Fatal("shape must share only immutable data, not runtime views")
		}
		return weak.Make(firstView.data)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if pointer.Value() == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unused shape data was retained by cache")
}

func TestGlobalOrderUsesPrivateClosureNames(t *testing.T) {
	shape, err := parseGlobalOrderShape(`{"top":{"global":[{"n":"before"},{"n":"closure_lm_123"},{"n":"after"}]},"frame":{"freshKeys":["globalThis","Function","before","closure_lm_123","after"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, create := range []func(*globalOrderShape, string) *firefoxGlobalOrder{newTopGlobalOrder, newFrameGlobalOrder} {
		first := create(shape, "closure_lm_42")
		second := create(shape, "closure_lm_99")
		first.Define("private")
		first.Delete("before")
		keys := []string{"after", "closure_lm_99", "before"}
		if got := second.OrderKeys(keys); !reflect.DeepEqual(got, []string{"before", "closure_lm_99", "after"}) {
			t.Fatalf("runtime order changed: %v", got)
		}
		if second.inNative["private"] || second.inNative["closure_lm_42"] {
			t.Fatal("global order state leaked across runtimes")
		}
	}
	if shape.Top.Global[1].N != "closure_lm_123" || shape.Frame.FreshKeys[3] != "closure_lm_123" {
		t.Fatal("shared global order data was mutated")
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
	state.closureName = "closure_lm_42"
	source = replaceClosureListenerMap(source, state.closureName)
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
