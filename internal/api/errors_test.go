package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestRetryAfterRounding(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		want  string
	}{
		{-time.Second, ""}, {0, ""}, {time.Nanosecond, "1"}, {time.Second, "1"},
		{time.Second + time.Nanosecond, "2"}, {1500 * time.Millisecond, "2"},
	} {
		w := httptest.NewRecorder()
		err := fmt.Errorf("wrapped: %w", &aistudio.RPCError{StatusCode: 429, RetryDelay: tc.delay})
		seconds := setRetryAfter(w, err)
		if got := w.Header().Get("Retry-After"); got != tc.want || (seconds == 0) != (tc.want == "") {
			t.Errorf("delay=%s seconds=%d header=%q want=%q", tc.delay, seconds, got, tc.want)
		}
	}
	for _, err := range []error{errors.New("no retry hint"), &aistudio.AllCoolingError{}, &aistudio.AllCoolingError{Until: time.Now().Add(-time.Hour)}} {
		w := httptest.NewRecorder()
		if setRetryAfter(w, err) != 0 || w.Header().Get("Retry-After") != "" {
			t.Errorf("unexpected retry hint for %v", err)
		}
	}
	until := time.Now().Add(time.Hour + 1500*time.Millisecond)
	w := httptest.NewRecorder()
	before := time.Now()
	seconds := setRetryAfter(w, fmt.Errorf("wrapped: %w", &aistudio.AllCoolingError{Until: until}))
	after := time.Now()
	minSeconds, maxSeconds := int64(math.Ceil(until.Sub(after).Seconds())), int64(math.Ceil(until.Sub(before).Seconds()))
	if seconds < minSeconds || seconds > maxSeconds || w.Header().Get("Retry-After") != strconv.FormatInt(seconds, 10) {
		t.Fatalf("AllCoolingError seconds=%d, want %d..%d; headers=%v", seconds, minSeconds, maxSeconds, w.Header())
	}
}

func TestRequestErrorPayloads(t *testing.T) {
	err := &aistudio.RPCError{StatusCode: 429, RetryDelay: 1500 * time.Millisecond, Message: "rate limited"}
	for _, tc := range []struct {
		name  string
		write func(http.ResponseWriter, error)
		want  map[string]any
	}{
		{"openai", writeOpenAIRequestError, map[string]any{"error": map[string]any{"message": err.Error(), "type": "api_error", "code": "upstream_error"}}},
		{"anthropic", writeAnthropicRequestError, map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": err.Error()}}},
		{"gemini", writeGeminiRequestError, map[string]any{"error": map[string]any{"code": float64(429), "message": err.Error(), "status": "RESOURCE_EXHAUSTED", "details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "2s"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.write(w, err)
			var got map[string]any
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, tc.want) || w.Code != 429 || w.Header().Get("Retry-After") != "2" {
				t.Fatalf("status=%d headers=%v body=%s want=%+v", w.Code, w.Header(), w.Body, tc.want)
			}
		})
	}
	w := httptest.NewRecorder()
	writeGeminiRequestError(w, &aistudio.RPCError{StatusCode: 429})
	if w.Header().Get("Retry-After") != "" || strings.Contains(w.Body.String(), "details") {
		t.Fatalf("invented retry hint: %s", w.Body)
	}
	w = httptest.NewRecorder()
	writeGeminiUploadFailure(w, &http.MaxBytesError{Limit: 1}, "final")
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if w.Code != 413 || w.Header().Get("X-Goog-Upload-Status") != "final" || payload["error"].(map[string]any)["status"] != "INVALID_ARGUMENT" || w.Header().Get("Retry-After") != "" {
		t.Fatalf("upload size error changed: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
	}
}

type requestErrorTestService struct {
	err          error
	modelsError  bool
	eventError   bool
	uploadOK     bool
	transcribeOK bool
}

func (s requestErrorTestService) Models(context.Context) ([]aistudio.Model, error) {
	if s.modelsError {
		return nil, s.err
	}
	return []aistudio.Model{{ID: "test", Methods: []string{"generateContent", "predictLongRunning"}}}, nil
}

func (s requestErrorTestService) Generate(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	if s.eventError {
		return testEventStream(aistudio.Event{Kind: aistudio.EventError, Err: s.err}), nil
	}
	return nil, s.err
}

func (s requestErrorTestService) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, s.err
}

func (s requestErrorTestService) Embed(context.Context, aistudio.EmbeddingRequest) (aistudio.EmbeddingResult, error) {
	return aistudio.EmbeddingResult{}, s.err
}

func (s requestErrorTestService) UploadFile(context.Context, aistudio.UploadRequest) (aistudio.FileRef, error) {
	if s.uploadOK {
		return aistudio.FileRef{ID: "file"}, nil
	}
	return aistudio.FileRef{}, s.err
}

func (s requestErrorTestService) FileMetadata(context.Context, string) (aistudio.FileMetadata, error) {
	return aistudio.FileMetadata{}, s.err
}

func (s requestErrorTestService) ListFiles(context.Context) ([]aistudio.FileMetadata, error) {
	return nil, s.err
}

func (s requestErrorTestService) DownloadFile(context.Context, string) (aistudio.MediaStream, error) {
	return aistudio.MediaStream{}, s.err
}

func (s requestErrorTestService) DeleteFile(context.Context, string) error { return s.err }

func (s requestErrorTestService) GenerateVideo(context.Context, aistudio.VideoRequest) (aistudio.VideoOperation, error) {
	return aistudio.VideoOperation{}, s.err
}

func (s requestErrorTestService) GetGenerateVideoOperation(context.Context, string) (aistudio.VideoOperation, error) {
	return aistudio.VideoOperation{}, s.err
}

func (s requestErrorTestService) ListVideos(context.Context) ([]aistudio.VideoOperation, error) {
	return nil, s.err
}

func (s requestErrorTestService) DeleteVideo(context.Context, string) error { return s.err }

func (s requestErrorTestService) Transcribe(context.Context, aistudio.TranscriptionRequest) (aistudio.TranscriptionResult, error) {
	if s.transcribeOK {
		return aistudio.TranscriptionResult{Text: "transcript"}, nil
	}
	return aistudio.TranscriptionResult{}, s.err
}

func TestRetryMetadataHTTP(t *testing.T) {
	failure := fmt.Errorf("wrapped: %w", &aistudio.RPCError{StatusCode: 429, RetryDelay: 1500 * time.Millisecond, Message: "limited"})
	assertRetry := func(t *testing.T, response *http.Response, raw []byte, gemini bool) {
		t.Helper()
		if response.StatusCode != 429 || response.Header.Get("Retry-After") != "2" {
			t.Fatalf("status=%d headers=%v body=%s", response.StatusCode, response.Header, raw)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		errBody := payload["error"].(map[string]any)
		if errBody["message"] != failure.Error() {
			t.Fatalf("upstream message lost: %s", raw)
		}
		if gemini && !reflect.DeepEqual(errBody["details"], []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "2s"}}) {
			t.Errorf("Gemini retry info=%+v", errBody)
		}
	}
	var multipartBody bytes.Buffer
	form := multipart.NewWriter(&multipartBody)
	for name, value := range map[string]string{"model": "test", "purpose": "user_data"} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := form.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("RIFF0000WAVEdata")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	multipartHeaders := http.Header{"Content-Type": {form.FormDataContentType()}}
	anthropicHeaders := http.Header{"Anthropic-Version": {"2023-06-01"}}
	for _, tc := range []struct {
		method, path, body, stage string
		headers                   http.Header
	}{
		{http.MethodGet, "/v1/models", "", "models", nil},
		{http.MethodGet, "/v1/models/test", "", "models", nil},
		{http.MethodGet, "/v1/models", "", "models", anthropicHeaders},
		{http.MethodGet, "/v1/models/test", "", "models", anthropicHeaders},
		{http.MethodGet, "/v1beta/models", "", "models", nil},
		{http.MethodGet, "/v1beta/models/test", "", "models", nil},
		{http.MethodPost, "/v1/responses/input_tokens", `{"model":"test","input":"hi"}`, "", nil},
		{http.MethodPost, "/v1/messages/count_tokens", `{"model":"test","messages":[{"role":"user","content":"hi"}]}`, "", nil},
		{http.MethodPost, "/v1beta/models/test:countTokens", `{"contents":[{"parts":[{"text":"hi"}]}]}`, "", nil},
		{http.MethodPost, "/v1/embeddings", `{"model":"test","input":"hi"}`, "", nil},
		{http.MethodPost, "/v1beta/models/test:embedContent", `{"content":{"parts":[{"text":"hi"}]}}`, "", nil},
		{http.MethodPost, "/v1/images/generations", `{"model":"test","prompt":"draw"}`, "models", nil},
		{http.MethodPost, "/v1/images/generations", `{"model":"test","prompt":"draw"}`, "", nil},
		{http.MethodPost, "/v1/audio/speech", `{"model":"test","input":"hi"}`, "", nil},
		{http.MethodPost, "/v1/audio/speech", `{"model":"test","input":"hi"}`, "event", nil},
		{http.MethodPost, "/v1/audio/transcriptions", multipartBody.String(), "", multipartHeaders},
		{http.MethodPost, "/v1/audio/translations", multipartBody.String(), "models", multipartHeaders},
		{http.MethodPost, "/v1/audio/translations", multipartBody.String(), "", multipartHeaders},
		{http.MethodPost, "/v1/audio/translations", multipartBody.String(), "translate", multipartHeaders},
		{http.MethodPost, "/v1/videos", `{"prompt":"draw"}`, "models", nil},
		{http.MethodPost, "/v1/videos", `{"model":"test","prompt":"draw"}`, "", nil},
		{http.MethodGet, "/v1/videos", "", "", nil},
		{http.MethodGet, "/v1/videos/video", "", "", nil},
		{http.MethodDelete, "/v1/videos/video", "", "", nil},
		{http.MethodPost, "/v1beta/models/test:predictLongRunning", `{"instances":[{"prompt":"draw"}]}`, "", nil},
		{http.MethodGet, "/v1beta/operations/video", "", "", nil},
		{http.MethodGet, "/v1/files", "", "", nil},
		{http.MethodGet, "/v1/files", "", "", anthropicHeaders},
		{http.MethodGet, "/v1/files/file", "", "", nil},
		{http.MethodDelete, "/v1/files/file", "", "", anthropicHeaders},
		{http.MethodPost, "/v1/files", multipartBody.String(), "", multipartHeaders},
		{http.MethodPost, "/v1/files", multipartBody.String(), "metadata", multipartHeaders},
		{http.MethodGet, "/v1beta/files", "", "", nil},
		{http.MethodGet, "/v1beta/files/" + geminiFileID("file"), "", "", nil},
		{http.MethodDelete, "/v1beta/files/" + geminiFileID("file"), "", "", nil},
	} {
		t.Run(tc.method+tc.path+"/"+tc.stage+tc.headers.Get("Anthropic-Version"), func(t *testing.T) {
			service := requestErrorTestService{err: failure, modelsError: tc.stage == "models", eventError: tc.stage == "event", uploadOK: tc.stage == "metadata", transcribeOK: tc.stage == "translate"}
			response, raw := protocolTestRequest(t, newProtocolTestServer(t, service), tc.method, tc.path, tc.body, tc.headers)
			assertRetry(t, response, raw, strings.HasPrefix(tc.path, "/v1beta/"))
		})
	}
	for _, eventError := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/interactions", "/v1beta/models/test:generateContent"} {
				t.Run(fmt.Sprintf("%s/event=%t/stream=%t", path, eventError, stream), func(t *testing.T) {
					if stream && strings.HasPrefix(path, "/v1beta/") {
						path = "/v1beta/models/test:streamGenerateContent?alt=sse"
					}
					body := fmt.Sprintf(`{"model":"test","messages":[{"role":"user","content":"hi"}],"input":"hi","contents":[{"parts":[{"text":"hi"}]}],"stream":%t}`, stream)
					response, raw := protocolTestRequest(t, newProtocolTestServer(t, requestErrorTestService{err: failure, eventError: eventError}), http.MethodPost, path, body, nil)
					assertRetry(t, response, raw, strings.HasPrefix(path, "/v1beta/") || path == "/v1/interactions")
				})
			}
		}
	}
	t.Run("resumable_upload", func(t *testing.T) {
		s := newProtocolTestServer(t, requestErrorTestService{err: failure})
		response, raw := protocolTestRequest(t, s, http.MethodPost, "/upload/v1beta/files", `{}`, http.Header{
			"X-Goog-Upload-Protocol": {"resumable"}, "X-Goog-Upload-Command": {"start"}, "X-Goog-Upload-Header-Content-Type": {"audio/wav"},
		})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("start upload: %d %s", response.StatusCode, raw)
		}
		path := strings.TrimPrefix(response.Header.Get("X-Goog-Upload-URL"), s.URL)
		response, raw = protocolTestRequest(t, s, http.MethodPost, path, "RIFF0000WAVEdata", http.Header{"X-Goog-Upload-Command": {"upload, finalize"}})
		assertRetry(t, response, raw, true)
		if response.Header.Get("X-Goog-Upload-Status") != "final" {
			t.Errorf("upload status header lost: %v", response.Header)
		}
	})
}

func TestValidationErrorsKeepProtocolShape(t *testing.T) {
	s := newProtocolTestServer(t, requestErrorTestService{err: &aistudio.RPCError{StatusCode: 429, RetryDelay: time.Second}})
	for _, tc := range []struct{ path, body, protocol string }{
		{"/v1/chat/completions", `{}`, "openai"},
		{"/v1/chat/completions", `{"model":"test","functions":[42]}`, "openai"},
		{"/v1/responses", `{"model":"test","reasoning":42}`, "openai"},
		{"/v1/responses", `{"model":"test","previous_response_id":"missing"}`, "openai"},
		{"/v1/images/generations", `{"model":"test","prompt":"draw","n":-1}`, "openai"},
		{"/v1/messages", `{"model":"test","thinking":{"type":"enabled"}}`, "anthropic"},
		{"/v1beta/models/test:generateContent", `{"generationConfig":{"candidateCount":99}}`, "gemini"},
		{"/v1beta/models/test:generateContent", `{"contents":[{"role":"bad","parts":[{"text":"hi"}]}]}`, "gemini"},
	} {
		t.Run(tc.path+"/"+tc.body, func(t *testing.T) {
			response, raw := protocolTestRequest(t, s, http.MethodPost, tc.path, tc.body, nil)
			if response.StatusCode != 400 || response.Header.Get("Retry-After") != "" {
				t.Fatalf("validation error changed: status=%d headers=%v body=%s", response.StatusCode, response.Header, raw)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			body := payload["error"].(map[string]any)
			switch tc.protocol {
			case "openai":
				if len(body) != 3 || body["type"] != "invalid_request_error" || body["code"] != "invalid_request" {
					t.Errorf("OpenAI validation shape=%s", raw)
				}
			case "anthropic":
				if len(body) != 2 || payload["type"] != "error" || body["type"] != "invalid_request_error" {
					t.Errorf("Anthropic validation shape=%s", raw)
				}
			case "gemini":
				if len(body) != 3 || body["code"] != float64(400) || body["status"] != "INVALID_ARGUMENT" {
					t.Errorf("Gemini validation shape=%s", raw)
				}
			}
		})
	}
}
