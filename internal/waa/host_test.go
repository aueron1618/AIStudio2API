package waa

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testRealmHost(t testing.TB, script string) *Runtime {
	t.Helper()
	vm, err := NewRuntime(t.Context(), Options{
		Challenge: Challenge{Program: script, GlobalName: "fixture"},
		Interpreter: `var fixture = {a(program, ready) {
  ready(callback => Promise.resolve((0, eval)(program)).then(
    value => callback('!' + JSON.stringify(value)),
    error => callback('!error:' + String(error))
  ), () => {});
}};`,
		Profile: Profile{Locale: "en-US", TimeZone: "UTC", PageURL: "https://aistudio.google.com/prompts/new_chat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func realmResult(t testing.TB, vm *Runtime) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	proof, err := vm.Proof(ctx, "digest", "")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(proof, "!")
}

func TestRealmHostIsolation(t *testing.T) {
	first := testRealmHost(t, `(() => {
  const iframe = document.createElement('iframe');
  document.body.appendChild(iframe);
  const frame = iframe.contentWindow;
  HTMLTextAreaElement.prototype.testMarker = 42;
  document.cookie = 'private=first';
  localStorage.setItem('private', 'first');
  document.body.appendChild(document.createElement('private-element'));
  return frame.Object !== Object;
})()`)
	defer first.Close()
	if result := realmResult(t, first); result != "true" {
		t.Fatalf("iframe did not have its own realm: %s", result)
	}
	second := testRealmHost(t, `HTMLTextAreaElement.prototype.testMarker === undefined &&
  !document.cookie.includes('private=first') &&
  localStorage.getItem('private') === null &&
  document.querySelector('private-element') === null`)
	defer second.Close()
	if result := realmResult(t, second); result != "true" {
		t.Fatalf("host state leaked between accounts: %s", result)
	}
}

func TestRealmHostGlobalKeys(t *testing.T) {
	vm := testRealmHost(t, `Object.getOwnPropertyNames(globalThis)`)
	defer vm.Close()
	var keys []string
	if err := json.Unmarshal([]byte(realmResult(t, vm)), &keys); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			t.Fatalf("duplicate global key %q", key)
		}
		seen[key] = true
	}
	for _, key := range []string{"window", "self", "document", "navigator", "Object", "Intl"} {
		if !seen[key] {
			t.Errorf("missing browser global %q", key)
		}
	}
}

func TestRealmHostUpstreamContracts(t *testing.T) {
	for _, test := range []struct{ name, script string }{
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
			vm := testRealmHost(t, test.script)
			defer vm.Close()
			if result := realmResult(t, vm); result != "true" {
				t.Fatalf("browser contract failed: %s", result)
			}
		})
	}
}

func BenchmarkRealmHost(b *testing.B) {
	testRealmHost(b, "true").Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		testRealmHost(b, "true").Close()
	}
}
