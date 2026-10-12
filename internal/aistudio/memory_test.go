package aistudio

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	sentencepiece "github.com/eliben/go-sentencepiece"
)

func TestTokenizerCacheIdleRelease(t *testing.T) {
	now := time.Now()
	cache := tokenizerCache{processor: new(sentencepiece.Processor), lastUsed: now}
	if cache.releaseBefore(now.Add(-time.Second)) {
		t.Fatal("released recently used tokenizer")
	}
	cache.users = 1
	if cache.releaseBefore(now.Add(time.Hour)) {
		t.Fatal("released tokenizer while encoding")
	}
	cache.users = 0
	if !cache.releaseBefore(now.Add(time.Second)) || cache.processor != nil {
		t.Fatal("idle tokenizer was retained")
	}
	if cache.releaseBefore(now.Add(time.Second)) {
		t.Fatal("empty cache reported a release")
	}
}

func TestTokenizerReloadPreservesCounts(t *testing.T) {
	texts := []string{"", "hello world", "你好，世界！", "code\n\treturn 42; 🦊"}
	counts := func() []int64 {
		result := make([]int64, len(texts))
		for i, text := range texts {
			result[i] = localTextTokens(text)
		}
		return result
	}
	first := counts()
	if !ReleaseIdleTokenizer(time.Now().Add(time.Second)) {
		t.Fatal("tokenizer not loaded")
	}
	second := counts()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("counts changed: %v -> %v", first, second)
	}
	ReleaseIdleTokenizer(time.Now().Add(time.Second))
	if localTextTokens("") != 0 {
		t.Fatal("empty text has tokens")
	}
	if ReleaseIdleTokenizer(time.Now().Add(time.Second)) {
		t.Fatal("empty text loaded large model")
	}
}

type idleCloseTransport struct{ closed int }

func (*idleCloseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("unexpected network")
}
func (transport *idleCloseTransport) CloseIdleConnections() { transport.closed++ }

func TestGoRuntimeCloseReleasesIdleConnections(t *testing.T) {
	transport := &idleCloseTransport{}
	runtime := &goWAARuntime{client: &http.Client{Transport: transport}, interpreter: "source", refreshTimer: time.NewTimer(time.Hour)}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if !runtime.closed || runtime.interpreter != "" || runtime.refreshTimer != nil || transport.closed != 1 {
		t.Fatal("closed runtime retained resources")
	}
}
