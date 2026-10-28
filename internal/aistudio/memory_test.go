package aistudio

import (
	"io"
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

type imageProbeBody struct {
	remaining, read, maxRead int
	closed                   bool
}

func (body *imageProbeBody) Read(buffer []byte) (int, error) {
	body.maxRead = max(body.maxRead, len(buffer))
	if body.remaining == 0 {
		return 0, io.EOF
	}
	size := min(len(buffer), body.remaining)
	clear(buffer[:size])
	body.remaining -= size
	body.read += size
	return size, nil
}

func (body *imageProbeBody) Close() error { body.closed = true; return nil }

type imageProbeTransport struct{ body *imageProbeBody }

func (transport imageProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}}, Body: transport.body, Request: request}, nil
}

func TestGoRuntimeImageProbeDiscardsStreamingBody(t *testing.T) {
	body := &imageProbeBody{remaining: 8 << 20}
	runtime := &goWAARuntime{client: &http.Client{Transport: imageProbeTransport{body: body}}}
	if !runtime.loadImage("https://aistudio.google.com/image.png") {
		t.Fatal("nonempty image was rejected")
	}
	if !body.closed || body.read != 4<<20 || body.maxRead > 32<<10 {
		t.Fatalf("image probe buffered or exceeded limit: closed=%v read=%d maxRead=%d", body.closed, body.read, body.maxRead)
	}
}
