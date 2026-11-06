package camoufoxnative

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/mozjs"
)

func TestHeadlessMemoryPreferences(t *testing.T) {
	prefs := headlessPreferences()
	if prefs["dom.ipc.processPrelaunch.enabled"] != false || prefs["browser.cache.memory.capacity"] != 16*1024 {
		t.Fatal("headless workers need bounded idle resources")
	}
	if prefs["layout.frame_rate"] != interactiveFrameRate || prefs["gfx.display.frame-rate-divisor"] != idleFrameDivisor {
		t.Fatal("headless workers lost idle division or interactive Run frame rate")
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
	if _, ok := base["gfx.display.frame-rate-divisor"]; ok {
		t.Fatal("headed login must not inherit the worker refresh divisor")
	}
}

func TestDecodeProtectedData(t *testing.T) {
	chunks := []string{"", "YQ==", "YmM=", "ZGVm"}
	var output []byte
	for _, chunk := range chunks {
		data, err := decodeProtectedData(chunk)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
	}
	if string(output) != "abcdef" {
		t.Fatalf("decoded = %q", output)
	}
	for _, invalid := range []string{"YQ==!", "!", "YQ", "YQ=", base64.StdEncoding.EncodeToString(make([]byte, protectedChunkBytes+1))} {
		if data, err := decodeProtectedData(invalid); err == nil || data != nil {
			t.Fatalf("invalid base64 %q must not return partial data", invalid)
		}
	}
	if data, err := decodeProtectedData(""); err != nil || data != nil {
		t.Fatal("empty chunks must not allocate a buffer")
	}
	if data, err := decodeProtectedData(base64.StdEncoding.EncodeToString(make([]byte, protectedChunkBytes))); err != nil || len(data) != protectedChunkBytes {
		t.Fatal("64 KiB boundary must be accepted")
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

// mockProtectedXHR executes the production scripts with explicit XHR progress.
// The encoder implements the standard encodeInto contract, including byte limits.
type protectedTestVM struct {
	cx     *mozjs.Context
	global mozjs.Handle
}

func mockProtectedXHR(t *testing.T) *protectedTestVM {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	cx, err := mozjs.NewContext(mozjs.Options{HeapBytes: 64 << 20, Locale: "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cx.Close)
	global, err := cx.NewGlobal(0, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	vm := &protectedTestVM{cx: cx, global: global}
	btoa, err := cx.NewFunction(global, "btoa", 1, func(call *mozjs.Call) error {
		value := call.ArgString(0)
		data := make([]byte, 0, len(value))
		for _, ch := range value {
			data = append(data, byte(ch))
		}
		call.Return(mozjs.String(base64.StdEncoding.EncodeToString(data)))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cx.Set(global, "btoa", mozjs.Object(btoa)); err != nil {
		t.Fatal(err)
	}
	cx.Release(btoa)
	runProtectedJS(t, vm, `
var page = {}, xhr, request, messages = [], sendError = false;
var listeners = new Map();
var addEventListener = (name, fn) => { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); };
var removeEventListener = (name, fn) => listeners.get(name)?.delete(fn);
var TextEncoder = class {
  encodeInto(text, target) {
    let read = 0, written = 0;
    for (const character of text) {
      let code = character.codePointAt(0);
      if (code >= 0xd800 && code <= 0xdfff) code = 0xfffd;
      const bytes = code < 0x80 ? [code] : code < 0x800 ? [0xc0 | code >> 6, 0x80 | code & 63] :
        code < 0x10000 ? [0xe0 | code >> 12, 0x80 | code >> 6 & 63, 0x80 | code & 63] :
        [0xf0 | code >> 18, 0x80 | code >> 12 & 63, 0x80 | code >> 6 & 63, 0x80 | code & 63];
      if (written + bytes.length > target.length) break;
      target.set(bytes, written);
      read += character.length;
      written += bytes.length;
    }
    return {read, written};
  }
};
var XMLHttpRequest = class {
  constructor() { xhr = this; this.readyState = 0; this.responseText = ''; this.status = 200; this.headers = []; this.listeners = new Map(); }
  addEventListener(name, fn) { if (!this.listeners.has(name)) this.listeners.set(name, new Set()); this.listeners.get(name).add(fn); }
  removeEventListener(name, fn) { this.listeners.get(name)?.delete(fn); }
  fire(name) { this['on' + name]?.(); for (const fn of this.listeners.get(name) || []) fn(); }
  open(method, url) { this.method = method; this.url = url; this.readyState = 1; }
  setRequestHeader(name, value) { this.headers.push([name, value]); }
  getAllResponseHeaders() { return 'Content-Type: application/json+protobuf\r\nX-Test: yes\r\n'; }
  send(body) {
    if (sendError) throw new Error('send failed');
    this.body = body;
    this.readyState = 2;
  }
  abort() {
    this.aborted = true;
    if (this.readyState > 0 && this.readyState < 4) this.fire('abort');
    this.readyState = 0;
    this.responseText = '';
  }
};
var advance = (text, done) => {
  xhr.responseText += text;
  xhr.readyState = done ? 4 : 3;
  xhr.fire('progress');
  if (done) xhr.fire('load');
};
`)
	runProtectedJS(t, vm, "("+protectedSend+")(page, 'test', 'https://example.test', '[[\"Content-Type\",\"application/json\"]]', 'body', '', value => messages.push(value))")
	runProtectedJS(t, vm, `request = page.requests.get('test')`)
	return vm
}

func runProtectedJS(t *testing.T, vm *protectedTestVM, source string) mozjs.Value {
	t.Helper()
	value, err := vm.cx.Evaluate(vm.global, source, "memory_test.js", 1)
	if err != nil {
		t.Fatal(err)
	}
	vm.cx.RunJobs()
	vm.cx.ReleaseValue(value)
	return value
}

func TestProtectedXHRBoundsLargeChunks(t *testing.T) {
	for name, input := range map[string]string{
		"ascii": strings.Repeat("a", 9*protectedChunkBytes+17),
		"utf8":  strings.Repeat("a\u4e2d\U00020000", 9*protectedChunkBytes/8+17),
	} {
		t.Run(name, func(t *testing.T) {
			vm := mockProtectedXHR(t)
			if err := vm.cx.Set(vm.global, "input", mozjs.String(input)); err != nil {
				t.Fatal(err)
			}
			runProtectedJS(t, vm, `advance(input, true)`)
			if n := runProtectedJS(t, vm, `messages.filter(value => !value.startsWith('{')).length`).Number; n != 8 {
				t.Fatalf("backpressure queue = %g, want 8", n)
			}
			if !runProtectedJS(t, vm, `request.credit === 0 && xhr.withCredentials && xhr.method === 'POST' && xhr.body === 'body'`).Bool {
				t.Fatal("request lost XHR semantics or flow control")
			}
			var output []byte
			done := false
			for range 10 {
				raw := runProtectedJS(t, vm, `JSON.stringify(messages.splice(0))`).String
				var messages []string
				if err := json.Unmarshal([]byte(raw), &messages); err != nil {
					t.Fatal(err)
				}
				chunks := 0
				for _, message := range messages {
					if strings.HasPrefix(message, "{") {
						var control protectedMessage
						if err := json.Unmarshal([]byte(message), &control); err != nil || control.Error != "" {
							t.Fatalf("control = %s, error = %v", message, err)
						}
						done = done || control.Done
						continue
					}
					chunks++
					data, err := base64.StdEncoding.DecodeString(message)
					if err != nil || len(data) > protectedChunkBytes {
						t.Fatalf("unbounded or invalid chunk: %d, %v", len(data), err)
					}
					output = append(output, data...)
				}
				if chunks > 8 {
					t.Fatal("too many queued chunks")
				}
				if done {
					break
				}
				runProtectedJS(t, vm, "("+protectedGrant+")(page, 'test', '"+strconv.Itoa(chunks)+"')")
			}
			if !done || !bytes.Equal(output, []byte(input)) {
				t.Fatalf("stream truncated or corrupted: %d of %d bytes", len(output), len(input))
			}
			if !runProtectedJS(t, vm, `page.requests.size === 0 && request.xhr === null && xhr.responseText === '' && xhr.onprogress === null`).Bool {
				t.Fatal("completed stream retained XHR data or callbacks")
			}
		})
	}
}

func TestProtectedXHRAbortWhileBackpressured(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		t.Run(strconv.FormatBool(loaded), func(t *testing.T) {
			vm := mockProtectedXHR(t)
			runProtectedJS(t, vm, `advance('x'.repeat(9 * 65536), `+strconv.FormatBool(loaded)+`)`)
			runProtectedJS(t, vm, `messages.length = 0`)
			runProtectedJS(t, vm, "("+protectedCancel+")(page, 'test')")
			if !runProtectedJS(t, vm, `page.requests.size === 0 && request.xhr === null && request.resume === null && xhr.responseText === '' && messages.length === 1 && JSON.parse(messages[0]).error.includes('AbortError')`).Bool {
				t.Fatal("aborted XHR retained data or kept producing")
			}
			runProtectedJS(t, vm, "("+protectedGrant+")(page, 'test', '8')")
			if runProtectedJS(t, vm, `messages.length`).Number != 1 {
				t.Fatal("grant revived an aborted XHR")
			}
		})
	}
}

func TestProtectedXHRNetworkErrorWhileBackpressured(t *testing.T) {
	vm := mockProtectedXHR(t)
	runProtectedJS(t, vm, `advance('x'.repeat(9 * 65536), false)`)
	if runProtectedJS(t, vm, `request.credit`).Number != 0 {
		t.Fatal("test requires an XHR suspended on credit")
	}
	runProtectedJS(t, vm, `messages.length = 0; xhr.onerror()`)
	if !runProtectedJS(t, vm, `page.requests.size === 0 && xhr.responseText === '' && messages.length === 1 && JSON.parse(messages[0]).error.includes('NetworkError')`).Bool {
		t.Fatal("network failure left a suspended XHR")
	}
}

func TestProtectedXHRPreservesSplitSurrogate(t *testing.T) {
	vm := mockProtectedXHR(t)
	runProtectedJS(t, vm, `advance('a\ud840', false)`)
	runProtectedJS(t, vm, `advance('\udc00b', true)`)
	raw := runProtectedJS(t, vm, `JSON.stringify(messages.filter(value => !value.startsWith('{')))`).String
	var chunks []string
	if err := json.Unmarshal([]byte(raw), &chunks); err != nil {
		t.Fatal(err)
	}
	var output []byte
	for _, chunk := range chunks {
		data, err := decodeProtectedData(chunk)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
	}
	if string(output) != "a\U00020000b" {
		t.Fatalf("split surrogate was corrupted: %q", output)
	}
}

func armGeneratedTestXHR(t *testing.T, vm *protectedTestVM, pending bool) {
	t.Helper()
	runProtectedJS(t, vm, "("+protectedCancel+")(page, 'test')")
	runProtectedJS(t, vm, `
var window = globalThis, finishProof;
var default_MakerSuite = {proof(service, digest) { page.digest = digest; return `+map[bool]string{true: `new Promise(resolve => finishProof = resolve)`, false: `Promise.resolve('!fresh')`}[pending]+`; }};
page = {key: 'proof', original: default_MakerSuite.proof};
messages = [];
var originalOpen = XMLHttpRequest.prototype.open;
`)
	runProtectedJS(t, vm, "("+generateArmFunction+")(page, 'test', 'binding', '[', ']', value => messages.push(value))")
	runProtectedJS(t, vm, `request = page.requests.get('test'); default_MakerSuite.proof({}, 'wrong').catch(() => {})`)
}

func TestGeneratePumpSharesByteBoundsAndReleasesCallbacks(t *testing.T) {
	vm := mockProtectedXHR(t)
	armGeneratedTestXHR(t, vm, false)
	runProtectedJS(t, vm, `xhr = new XMLHttpRequest(); xhr.open('POST', '`+aiStudioOrigin+generateContentPath+`'); xhr.send('old-body')`)
	if !runProtectedJS(t, vm, `xhr.body === '["!fresh"]' && page.digest === 'binding' && JSON.parse(messages[0]).sent`).Bool {
		t.Fatal("Run did not use the page proof or replace the wire proof field")
	}
	input := strings.Repeat("a\u4e2d\U00020000", 10*protectedChunkBytes/8)
	if err := vm.cx.Set(vm.global, "input", mozjs.String(input)); err != nil {
		t.Fatal(err)
	}
	runProtectedJS(t, vm, `advance(input, true)`)
	if n := runProtectedJS(t, vm, `messages.filter(value => !value.startsWith('{')).length`).Number; n != 8 {
		t.Fatalf("Generate queued %g chunks before credit", n)
	}
	runProtectedJS(t, vm, "("+protectedGrant+")(page, 'test', '4')")
	var messages []string
	if err := json.Unmarshal([]byte(runProtectedJS(t, vm, `JSON.stringify(messages)`).String), &messages); err != nil {
		t.Fatal(err)
	}
	var output []byte
	done := false
	for _, value := range messages {
		if strings.HasPrefix(value, "{") {
			var message protectedMessage
			if err := json.Unmarshal([]byte(value), &message); err != nil || message.Error != "" {
				t.Fatalf("Generate control %s: %v", value, err)
			}
			done = done || message.Done
			continue
		}
		data, err := decodeProtectedData(value)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, data...)
	}
	if !done || string(output) != input {
		t.Fatal("Generate stream was truncated or crossed UTF-8 byte boundaries")
	}
	if !runProtectedJS(t, vm, `request.xhr === null && request.cancel === null && page.requests.size === 0 && xhr.responseText === '' && [...xhr.listeners.values()].every(value => value.size === 0) && XMLHttpRequest.prototype.open === originalOpen`).Bool {
		t.Fatal("Generate completion retained callbacks, request roots, or hooks")
	}
}

func TestGenerateCancelDoesNotRearmLateProof(t *testing.T) {
	vm := mockProtectedXHR(t)
	armGeneratedTestXHR(t, vm, true)
	runProtectedJS(t, vm, "("+protectedCancel+")(page, 'test')")
	runProtectedJS(t, vm, `finishProof('!late')`)
	if !runProtectedJS(t, vm, `default_MakerSuite.proof === page.original && XMLHttpRequest.prototype.open === originalOpen && request.xhr === null && page.requests.size === 0 && messages.length === 1 && JSON.parse(messages[0]).error.includes('AbortError')`).Bool {
		t.Fatal("late proof reinstalled a canceled Run hook")
	}
}

func TestGenerateFailureWakesBackpressureAndDetachesListeners(t *testing.T) {
	for _, failure := range []string{"error", "abort", "send"} {
		t.Run(failure, func(t *testing.T) {
			vm := mockProtectedXHR(t)
			armGeneratedTestXHR(t, vm, false)
			if failure == "send" {
				runProtectedJS(t, vm, `sendError = true`)
			}
			runProtectedJS(t, vm, `xhr = new XMLHttpRequest(); xhr.open('POST', '`+aiStudioOrigin+generateContentPath+`'); try { xhr.send('body') } catch (_) {}`)
			if failure != "send" {
				runProtectedJS(t, vm, `advance('x'.repeat(9 * 65536), false); messages = []; xhr.fire('`+failure+`')`)
			}
			if !runProtectedJS(t, vm, `request.xhr === null && request.resume === null && page.requests.size === 0 && xhr.responseText === '' && [...xhr.listeners.values()].every(value => value.size === 0) && messages.some(value => value.startsWith('{') && JSON.parse(value).error)`).Bool {
				t.Fatal("failed Generate retained an XHR or its callback closures")
			}
		})
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
	b.Run("streamed-blocks", func(b *testing.B) {
		b.SetBytes(8 * 65536)
		b.ReportAllocs()
		for b.Loop() {
			for _, chunk := range chunks {
				if _, err := decodeProtectedData(chunk); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
