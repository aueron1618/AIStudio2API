package waa

import (
	"slices"
	"testing"
	"time"

	"github.com/Mag1cFall/goja"
)

func newTestHostState() (*goja.Runtime, *hostState) {
	vm := goja.New()
	state := &hostState{
		registry:   newHostRegistry(),
		rejections: &rejectionQueue{},
		profile:    Profile{Locale: "en-US", TimeZone: "UTC", PageURL: "https://aistudio.google.com/prompts/new_chat"},
		location:   time.UTC,
		schedule:   func(time.Duration, func()) {},
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
	if _, err := first.RunString(`
HTMLTextAreaElement.prototype.testMarker = 42;
Object.defineProperty(HTMLTextAreaElement.prototype, 'value', { configurable: true, get() { return 'leaked'; } });
document.cookie = 'private=first';
localStorage.setItem('private', 'first');
document.body.appendChild(document.createElement('private-element'));
`); err != nil {
		t.Fatal(err)
	}
	second, _ := testRealmHost(t)
	value, err := second.RunString(`
HTMLTextAreaElement.prototype.testMarker === undefined &&
document.createElement('textarea').value === '' &&
!document.cookie.includes('private=first') &&
localStorage.getItem('private') === null &&
document.querySelector('private-element') === null
`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("host state leaked across VMs: %v, %v", value, err)
	}
}

func TestRealmHostClosureNameAndOrder(t *testing.T) {
	for range 2 {
		vm, state := testRealmHost(t)
		value, err := vm.RunString(`Object.getOwnPropertyNames(globalThis)`)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		if err := vm.ExportTo(value, &keys); err != nil {
			t.Fatal(err)
		}
		at := slices.Index(keys, state.closureName)
		if at < 0 || !closureListenerMapPattern.MatchString(state.closureName) {
			t.Fatalf("runtime listener name is missing: %q", state.closureName)
		}
		previous := -1
		for _, entry := range state.order.Top.Global {
			name := replaceClosureListenerMap(entry.N, state.closureName)
			if name == "undefined" {
				continue
			}
			if index := slices.Index(keys, name); index >= 0 {
				if index <= previous {
					t.Fatalf("global %q is out of captured order: index %d after %d", name, index, previous)
				}
				previous = index
			}
		}
	}
}

func TestRealmHostUpstreamContracts(t *testing.T) {
	vm, _ := testRealmHost(t)
	for _, test := range []struct {
		name, script string
	}{
		{"URL", `(() => {
  const url = new URL('../\u00e9?q=a b', 'https://example.com/a/');
  if (url.href !== 'https://example.com/%C3%A9?q=a%20b') return false;
  url.searchParams.append('x', 'a+b');
  url.port = '443';
  return url.host === 'example.com' && url.search === '?q=a+b&x=a%2Bb' && URL.canParse(url.href) && !URL.canParse('http://[');
})()`},
		{"TextEncoding", `(() => {
  const bytes = new TextEncoder().encode('\u{1f98a}');
  if (new TextDecoder('utf-8', { fatal: true }).decode(bytes) !== '\u{1f98a}') return false;
  try { new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array([255])); } catch (error) { return error instanceof TypeError; }
  return false;
})()`},
		{"ArrayBufferTransfer", `(() => {
  const source = new Uint8Array([1, 2, 3]).buffer;
  const copied = source.transfer(5);
  return source.detached && copied.byteLength === 5 && Array.from(new Uint8Array(copied)).join(',') === '1,2,3,0,0';
})()`},
		{"Digest", `crypto.subtle.digest('SHA-256', new TextEncoder().encode('abc')).then(buffer => Array.from(new Uint8Array(buffer), byte => byte.toString(16).padStart(2, '0')).join('') === 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')`},
		{"IntlSegmenter", `(() => {
  const segments = new Intl.Segmenter('en-US', { granularity: 'grapheme' }).segment('a\u0301\u{1f469}\u200d\u{1f4bb}Z');
  if (Array.from(segments, item => item.index).join(',') !== '0,2,7' || segments.containing(5).index !== 2) return false;
  const words = Array.from(new Intl.Segmenter('en-US', { granularity: 'word' }).segment('Hello, world!'));
  return words.filter(item => item.isWordLike).map(item => item.segment).join(',') === 'Hello,world';
})()`},
		{"IntlLikelySubtags", `new Intl.Locale('en').maximize().toString() === 'en-Latn-US'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := vm.RunString(test.script)
			if err != nil {
				t.Fatal(err)
			}
			if promise, ok := value.Export().(*goja.Promise); ok {
				if promise.State() != goja.PromiseStateFulfilled {
					t.Fatalf("host promise did not fulfill: %v", promise.Result())
				}
				value = promise.Result()
			}
			if !value.ToBoolean() {
				t.Fatalf("host contract failed: %v", value)
			}
		})
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
