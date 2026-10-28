package waa

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/Mag1cFall/goja"
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

func TestTrustedScriptFunctionAndTimerSinks(t *testing.T) {
	vm := goja.New()
	registry := newHostRegistry()
	vm.SetEvalTransformer(registry.evalTransformer(vm, true))
	vm.SetFunctionTransformer(registry.functionTransformer(vm, true))
	for name, source := range map[string]string{"trustedExpression": "1 + 2", "trustedArgument": "value", "trustedBody": "return value + 4", "trustedTimer": "globalThis.timerRan = true"} {
		object := vm.NewObject()
		registry.trusted.set(object, source)
		if err := vm.Set(name, object); err != nil {
			t.Fatal(err)
		}
	}
	var jobs []func()
	timers := &realmTimers{
		vm: vm, registry: registry, trustedTypes: true, active: make(map[int64]bool),
		schedule: func(_ time.Duration, job func()) { jobs = append(jobs, job) },
		report: func(err error) {
			if err != nil {
				t.Errorf("timer failed: %v", err)
			}
		},
	}
	if err := vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value { return timers.start(call, false) }); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString(`(() => {
  if (eval(trustedExpression) !== 3 || new Function(trustedArgument, trustedBody)(3) !== 7) return false;
  const blocked = (fn, name) => { try { fn(); } catch (error) { return error.name === name; } return false; };
  if (!blocked(() => eval('1 + 2'), 'EvalError')) return false;
  if (!blocked(() => new Function('value', 'return value'), 'EvalError')) return false;
  if (!blocked(() => setTimeout('timerRan = false', 0), 'TypeError')) return false;
  setTimeout(trustedTimer, 0);
  return true;
})()`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("Trusted Types sinks failed: %v, %v", value, err)
	}
	if len(jobs) != 1 {
		t.Fatalf("unexpected accepted timers: %d", len(jobs))
	}
	jobs[0]()
	if value := vm.Get("timerRan"); value == nil || !value.ToBoolean() {
		t.Fatal("trusted timer did not execute")
	}
}
