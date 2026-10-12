package camoufoxnative

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja"
)

func TestHeadlessMemoryPreferences(t *testing.T) {
	prefs := headlessPreferences()
	if prefs["dom.ipc.processPrelaunch.enabled"] != false || prefs["browser.cache.memory.capacity"] != 16*1024 {
		t.Fatal("headless workers need bounded idle resources")
	}
	for key, value := range prefs {
		if _, err := firefoxPrefLiteral(value); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	for _, key := range []string{"fission.autostart", "security.sandbox.content.level", "webgl.disabled", "permissions.default.image"} {
		if _, ok := prefs[key]; ok {
			t.Fatalf("memory preferences must not override %s", key)
		}
	}
	base, err := firefoxPreferences("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base["browser.cache.memory.capacity"]; ok {
		t.Fatal("headed login must keep its default cache preferences")
	}
}

func TestDecodeProtectedData(t *testing.T) {
	chunks := []string{"", "YQ==", "YmM=", "ZGVm"}
	data, err := decodeProtectedData(chunks)
	if err != nil || string(data) != "abcdef" {
		t.Fatalf("decoded = %q, %v", data, err)
	}
	for _, invalid := range [][]string{{"YQ==", "!"}, {"!"}, {"YQ"}, {"YQ="}} {
		if data, err := decodeProtectedData(invalid); err == nil || data != nil {
			t.Fatalf("invalid base64 %q must not return partial data", invalid)
		}
	}
	if data, err := decodeProtectedData(nil); err != nil || data != nil {
		t.Fatal("empty chunks must not allocate a buffer")
	}
}

func TestProtectedBodyReleasesBuffer(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	body := &protectedResponseBody{buffer: []byte("abc"), done: true, cancel: cancel}
	defer body.Close()
	if n, err := body.Read(nil); n != 0 || err != nil || len(body.buffer) != 3 {
		t.Fatal("empty read consumed data")
	}
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "abc" || body.buffer != nil {
		t.Fatalf("read = %q, %v; retained buffer = %v", got, err, body.buffer)
	}
	body.buffer = make([]byte, 1024)
	if err := body.Close(); err != nil || body.buffer != nil {
		t.Fatal("close retained unread data")
	}
}

// mockProtectedFetch executes the actual browser script with a deterministic stream.
// No browser, credentials, network or wall-clock timers are needed.
func mockProtectedFetch(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	if err := vm.Set("btoa", func(value string) string {
		data := make([]byte, 0, len(value))
		for _, ch := range value {
			data = append(data, byte(ch))
		}
		return base64.StdEncoding.EncodeToString(data)
	}); err != nil {
		t.Fatal(err)
	}
	runProtectedJS(t, vm, `
var window = globalThis;
var timers = [], reads = 0, released = false;
var AbortController = class {
  constructor() { this.signal = {aborted: false}; }
  abort() { this.signal.aborted = true; }
};
var DOMException = Error;
var setTimeout = callback => timers.push(callback);
var input = new Uint8Array(9 * 65536 + 17);
for (let i = 0; i < input.length; i++) input[i] = i % 256;
var fetch = async () => ({status: 200, headers: new Map(), body: {
  getReader: () => ({
    read: async () => reads++ === 0 ? {value: input, done: false} : {done: true},
    releaseLock: () => { released = true; }
  })
}});
`)
	runProtectedJS(t, vm, "("+protectedFetch+")('test', 'https://example.test', '{}', '')")
	return vm
}

func runProtectedJS(t *testing.T, vm *goja.Runtime, source string) goja.Value {
	t.Helper()
	value, err := vm.RunString(source)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestProtectedFetchBoundsLargeChunks(t *testing.T) {
	vm := mockProtectedFetch(t)
	if n := runProtectedJS(t, vm, `window.__aistudioProtectedRequests.get('test').chunks.length`).ToInteger(); n != 8 {
		t.Fatalf("backpressure queue = %d, want 8", n)
	}
	if reads := vm.Get("reads").ToInteger(); reads != 1 {
		t.Fatalf("read ahead despite backpressure: %d", reads)
	}
	var output []byte
	for range 10 {
		raw := runProtectedJS(t, vm, `JSON.stringify(window.__aistudioProtectedRequests.get('test').chunks.splice(0))`).String()
		var chunks []string
		if err := json.Unmarshal([]byte(raw), &chunks); err != nil {
			t.Fatal(err)
		}
		if len(chunks) > 8 {
			t.Fatal("too many queued chunks")
		}
		for _, chunk := range chunks {
			data, err := base64.StdEncoding.DecodeString(chunk)
			if err != nil || len(data) > 65536 {
				t.Fatalf("unbounded or invalid chunk: %d, %v", len(data), err)
			}
			output = append(output, data...)
		}
		if runProtectedJS(t, vm, `window.__aistudioProtectedRequests.get('test').done`).ToBoolean() {
			break
		}
		runProtectedJS(t, vm, `timers.splice(0).forEach(callback => callback())`)
	}
	want := make([]byte, 9*65536+17)
	for i := range want {
		want[i] = byte(i)
	}
	if !bytes.Equal(output, want) || !vm.Get("released").ToBoolean() {
		t.Fatalf("stream truncated/corrupted or reader retained: %d bytes", len(output))
	}
}

func TestProtectedFetchAbortWhileBackpressured(t *testing.T) {
	vm := mockProtectedFetch(t)
	runProtectedJS(t, vm, `
var state = window.__aistudioProtectedRequests.get('test');
state.controller.abort();
state.chunks.length = 0;
window.__aistudioProtectedRequests.delete('test');
timers.splice(0).forEach(callback => callback());
`)
	if !runProtectedJS(t, vm, `state.done && state.error !== "" && state.chunks.length === 0 && released`).ToBoolean() {
		t.Fatal("aborted stream retained data or kept producing")
	}
}

func BenchmarkDecodeProtectedData(b *testing.B) {
	chunk := base64.StdEncoding.EncodeToString(make([]byte, 65536))
	chunks := []string{chunk, chunk, chunk, chunk, chunk, chunk, chunk, chunk}
	// 保留优化前的逐块扩容作为基线，便于在同一进程内复现分配量对比。
	b.Run("append-growth", func(b *testing.B) {
		b.SetBytes(8 * 65536)
		b.ReportAllocs()
		for b.Loop() {
			var buffer []byte
			for _, chunk := range chunks {
				var err error
				buffer, err = base64.StdEncoding.AppendDecode(buffer, []byte(chunk))
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("preallocated", func(b *testing.B) {
		b.SetBytes(8 * 65536)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := decodeProtectedData(chunks); err != nil {
				b.Fatal(err)
			}
		}
	})
}
