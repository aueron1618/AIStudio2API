package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestAuthMiddlewarePriorityAndMatching(t *testing.T) {
	requiredKey := "secret-123"
	handler := authMiddleware(requiredKey, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		url        string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "query key matches",
			url:        "/v1beta/models/gemini?key=secret-123",
			headers:    nil,
			wantStatus: http.StatusOK,
		},
		{
			name:       "x-goog-api-key matches while query has bogus token (SillyTavern + New API)",
			url:        "/v1beta/models/gemini?key=sk-user-bogus-token&alt=sse",
			headers:    map[string]string{"X-Goog-API-Key": "secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "bearer token matches while query has bogus token",
			url:        "/v1beta/models/gemini?key=sk-user-bogus-token",
			headers:    map[string]string{"Authorization": "Bearer secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "x-api-key matches",
			url:        "/v1/messages",
			headers:    map[string]string{"X-API-Key": "secret-123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "all mismatched",
			url:        "/v1beta/models/gemini?key=wrong-key",
			headers:    map[string]string{"X-Goog-API-Key": "wrong-header"},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.url, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

type modelLogAdmin struct {
	AdminService
	entry AccessLog
}

func (admin *modelLogAdmin) RecordAccessStart(AccessLog) {}

func (admin *modelLogAdmin) RecordAccessLog(entry AccessLog) {
	admin.entry = entry
}

type modelLogLedger struct {
	RequestLedger
	enabled bool
	body    RequestBody
}

func (ledger *modelLogLedger) BodyCapture() bool { return ledger.enabled }

func (ledger *modelLogLedger) SaveBody(body RequestBody) { ledger.body = body }

type modelLogService struct {
	aistudio.Service
	err error
}

func (service modelLogService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	SetAccessLogTarget(ctx, request.Model, "")
	SetAccessLogTarget(ctx, "canonical-model", "account")
	SetAccessLogTarget(ctx, "provider-model", "account")
	if service.err != nil {
		return nil, service.err
	}
	events := make(chan aistudio.Event, 2)
	events <- aistudio.Event{Kind: aistudio.EventText, Text: "hello", ProviderModel: "provider-model"}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop", ProviderModel: "provider-model"}
	close(events)
	return events, nil
}

func TestAccessLogModels(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, requested, response string
	}{
		{"chat", "/v1/chat/completions", `"model":"models/requested-alias","messages":[{"role":"user","content":"hi"}]`, "models/requested-alias", "models/requested-alias"},
		{"build-chat", "/build/v1/chat/completions", `"model":"models/requested-alias","messages":[{"role":"user","content":"hi"}]`, "models/requested-alias", "models/requested-alias"},
		{"responses", "/v1/responses", `"model":"models/requested-alias","input":"hi"`, "models/requested-alias", "models/requested-alias"},
		{"anthropic", "/v1/messages", `"model":"models/requested-alias","max_tokens":16,"messages":[{"role":"user","content":"hi"}]`, "models/requested-alias", "models/requested-alias"},
		{"interactions", "/v1beta/interactions", `"model":"models/requested-alias","input":"hi"`, "models/requested-alias", "requested-alias"},
		{"gemini", "/v1beta/models/requested-alias:generateContent", `"contents":[{"parts":[{"text":"hi"}]}]`, "requested-alias", ""},
	} {
		for _, stream := range []bool{false, true} {
			for _, capture := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/capture=%t", tc.name, stream, capture), func(t *testing.T) {
					admin := &modelLogAdmin{}
					ledger := &modelLogLedger{enabled: capture}
					handler := NewHandler(modelLogService{}, Config{Admin: admin, Ledger: ledger})
					path := tc.path
					if stream {
						path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
					}
					r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf(`{%s,"stream":%t}`, tc.body, stream)))
					r.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("status=%d: %s", w.Code, w.Body)
					}
					entry := admin.entry
					if entry.RequestedModel != tc.requested || entry.ResponseModel != tc.response || entry.Model != "provider-model" {
						t.Fatalf("requested=%q response=%q target=%q", entry.RequestedModel, entry.ResponseModel, entry.Model)
					}
					if tc.response != "" && !strings.Contains(w.Body.String(), `"model":"`+tc.response+`"`) {
						t.Fatalf("logged model missing from emitted JSON: %s", w.Body)
					}
					if (ledger.body.Response != "") != capture {
						t.Fatalf("body capture=%t, body=%q", capture, ledger.body.Response)
					}
				})
			}
		}
	}
}

func TestAccessLogModelsOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"upstream", "/v1/chat/completions", `{"model":"requested-alias","messages":[{"role":"user","content":"hi"}]}`, http.StatusBadGateway},
		{"validation", "/v1/embeddings", `{"model":"requested-alias","input":"hi","encoding_format":"invalid"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin := &modelLogAdmin{}
			handler := NewHandler(modelLogService{err: errors.New("upstream unavailable")}, Config{Admin: admin})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || admin.entry.RequestedModel != "requested-alias" || admin.entry.ResponseModel != "" {
				t.Fatalf("status=%d requested=%q response=%q: %s", w.Code, admin.entry.RequestedModel, admin.entry.ResponseModel, w.Body)
			}
		})
	}
}

func TestAccessLogResponseModelAfterTruncatedStream(t *testing.T) {
	admin := &modelLogAdmin{}
	ledger := &modelLogLedger{enabled: true}
	handler := requestLoggingMiddleware(admin, requestBodyMiddleware(ledger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setAccessLogRequestedModel(r.Context(), "requested-alias")
		if err := streamHeaders(w); err != nil {
			t.Fatal(err)
		}
		for _, frame := range []struct {
			event   string
			payload map[string]any
		}{
			{"", map[string]any{"model": "first-model"}},
			{"", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": strings.Repeat("x", requestBodyLimit)}}}}},
			{"", map[string]any{"model": "models/final-model"}},
			{"", map[string]any{"provider_model": "not-the-response-model", "modelVersion": "not-model"}},
			{"response.output_item.added", map[string]any{"item": map[string]any{"model": "tool-model"}}},
			{"", map[string]any{"error": map[string]any{"model": "error-model"}}},
			{"", map[string]any{"model": nil}},
		} {
			if err := writeSSE(w, frame.event, frame.payload); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeSSEHeartbeat(w); err != nil {
			t.Fatal(err)
		}
		if err := writeSSEText(w, "[DONE]"); err != nil {
			t.Fatal(err)
		}
	})))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if admin.entry.RequestedModel != "requested-alias" || admin.entry.ResponseModel != "models/final-model" {
		t.Fatalf("requested=%q response=%q", admin.entry.RequestedModel, admin.entry.ResponseModel)
	}
	if len(ledger.body.Response) != requestBodyLimit || strings.Contains(ledger.body.Response, "final-model") {
		t.Fatal("expected the final model to be outside the saved response body")
	}
}

type modelLogFailedWriter struct{ http.ResponseWriter }

func (modelLogFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAccessLogResponseModelRequiresSuccessfulWrite(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, encodeError := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/encodeError=%t", stream, encodeError), func(t *testing.T) {
				metadata := &accessLogMetadata{responseModel: "previous-model"}
				var output http.ResponseWriter = httptest.NewRecorder()
				payload := map[string]any{"model": "unsent-model"}
				if encodeError {
					payload["invalid"] = make(chan int)
				} else {
					output = modelLogFailedWriter{ResponseWriter: output}
				}
				writer := &accessLogResponseWriter{ResponseWriter: output, metadata: metadata}
				if stream {
					if err := writeSSE(writer, "", payload); err == nil {
						t.Fatal("expected SSE write to fail")
					}
				} else {
					writeJSON(writer, http.StatusOK, payload)
				}
				if model := metadata.snapshot().responseModel; model != "previous-model" {
					t.Fatalf("captured unwritten model %q", model)
				}
			})
		}
	}
}
