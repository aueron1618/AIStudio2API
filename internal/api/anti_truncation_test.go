package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type antiTruncationTestService struct {
	generationTestService
	models  []aistudio.Model
	counted aistudio.TokenCountRequest
}

func (service *antiTruncationTestService) Models(context.Context) ([]aistudio.Model, error) {
	return service.models, nil
}

func (service *antiTruncationTestService) CountTokens(_ context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	service.counted = request
	return aistudio.TokenCount{InputTokens: 7}, nil
}

func TestAntiTruncationModelLists(t *testing.T) {
	service := &antiTruncationTestService{models: []aistudio.Model{
		{ID: "test", Name: "Test", Methods: []string{"generateContent"}, Channels: []string{"playground", "build"},
			InputTokenLimit: 100, OutputTokenLimit: 20,
			Capabilities:      map[string]bool{"chat_model": true, "google_search": true},
			CapabilityOptions: map[string][]string{"aliases": {"alias"}}},
		{ID: "mapped", Name: "Mapped", Methods: []string{"generateContent"}, Channels: []string{"playground"}},
		{ID: "embedding", Methods: []string{"embedContent"}, Channels: []string{"build"}},
		{ID: antiTruncationModelPrefix + "reserved", Channels: []string{"playground", "build"}},
	}}
	before, _ := json.Marshal(service.models)
	var enabled atomic.Bool
	handler := NewHandler(service, Config{AntiTruncation: enabled.Load})
	for _, path := range []string{"/v1/models", "/build/v1/models", "/v1beta/models"} {
		for _, anthropic := range []bool{false, true} {
			for _, on := range []bool{false, true, false} {
				enabled.Store(on)
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if anthropic {
					r.Header.Set("Anthropic-Version", "2023-06-01")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status=%d: %s", path, w.Code, w.Body)
				}
				var payload struct {
					Data   []map[string]any `json:"data"`
					Models []map[string]any `json:"models"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				items := append(payload.Data, payload.Models...)
				ids := make(map[string]bool)
				for _, item := range items {
					id, _ := item["id"].(string)
					if id == "" {
						id = strings.TrimPrefix(item["name"].(string), "models/")
					}
					if ids[id] {
						t.Fatalf("duplicate model %q", id)
					}
					ids[id] = true
				}
				for id := range ids {
					if strings.HasPrefix(id, antiTruncationModelPrefix) {
						if !on || !ids[strings.TrimPrefix(id, antiTruncationModelPrefix)] {
							t.Fatalf("unexpected prefixed model %q: %v", id, ids)
						}
					} else if on && !ids[antiTruncationModelPrefix+id] {
						t.Fatalf("model %q has no anti-truncation variant: %v", id, ids)
					}
				}
				if !ids["test"] || !ids["test-search"] || ids[antiTruncationModelPrefix+"reserved"] {
					t.Fatalf("original/search models changed: %v", ids)
				}
				if path == "/v1/models" && ids["embedding"] || path == "/build/v1/models" && ids["mapped"] {
					t.Fatalf("channel filter changed: %v", ids)
				}
			}
		}
	}
	after, _ := json.Marshal(service.models)
	if !bytes.Equal(before, after) {
		t.Fatal("model projection mutated the real catalog")
	}
	for _, on := range []bool{true, false} {
		enabled.Store(on)
		for _, prefix := range []string{"/v1/models/", "/v1beta/models/"} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, prefix+url.PathEscape(antiTruncationModelPrefix+"test"), nil))
			want := http.StatusNotFound
			if on {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("single model enabled=%t: status=%d: %s", on, w.Code, w.Body)
			}
		}
	}
}

func antiTruncationProtocolRequest(protocol, model string, stream bool) (string, string) {
	switch protocol {
	case "chat":
		return "/v1/chat/completions", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":%t,"stream_options":{"include_usage":true}}`, model, stream)
	case "messages":
		return "/v1/messages", fmt.Sprintf(`{"model":%q,"max_tokens":256,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, model, stream)
	case "responses":
		return "/v1/responses", fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, model, stream)
	case "interactions":
		return "/v1beta/interactions", fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, model, stream)
	default:
		action := ":generateContent"
		if stream {
			action = ":streamGenerateContent?alt=sse"
		}
		return "/v1beta/models/" + url.PathEscape(model) + action, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	}
}

func TestAntiTruncationRequestRouting(t *testing.T) {
	for _, protocol := range []string{"chat", "messages", "responses", "gemini", "interactions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				requests := make(chan aistudio.GenerateRequest, 1)
				service := generationTestService{generate: func(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					requests <- request
					return testEventStream(aistudio.Event{Kind: aistudio.EventText, Text: "answer"}, aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "STOP"}), nil
				}}
				var enabled atomic.Bool
				handler := NewHandler(service, Config{AntiTruncation: enabled.Load})
				for _, on := range []bool{false, true, false} {
					enabled.Store(on)
					for _, prefixed := range []bool{false, true} {
						model := "test-search(high)"
						if prefixed {
							model = antiTruncationModelPrefix + model
						}
						path, body := antiTruncationProtocolRequest(protocol, model, stream)
						w := httptest.NewRecorder()
						handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
						if prefixed && !on {
							if w.Code != http.StatusNotFound || len(requests) != 0 {
								t.Fatalf("disabled prefix reached upstream: status=%d: %s", w.Code, w.Body)
							}
							continue
						}
						if w.Code != http.StatusOK {
							t.Fatalf("status=%d: %s", w.Code, w.Body)
						}
						if len(requests) == 0 {
							t.Fatalf("generation was not started: %s", w.Body)
						}
						request := <-requests
						if request.Model != "test(high)" || request.AntiTruncation != prefixed || request.Tools.GoogleSearch == nil {
							t.Fatalf("incorrect routing: %+v", request)
						}
						if len(request.System) != 0 || len(request.Tools.Functions) != 0 {
							t.Fatal("API alias handling must not mutate the logical conversation")
						}
					}
				}
			})
		}
	}
}

func TestAntiTruncationResolutionAndCountTokens(t *testing.T) {
	service := &antiTruncationTestService{}
	s := &server{service: service, config: Config{AntiTruncation: func() bool { return true }}}
	for _, model := range []string{antiTruncationModelPrefix + "mapped(high)", "models/" + antiTruncationModelPrefix + "mapped(high)"} {
		count, err := s.countTokens(t.Context(), aistudio.TokenCountRequest{Model: model, System: []string{"unchanged"}})
		if err != nil || count.InputTokens != 7 || service.counted.Model != "mapped(high)" || !reflect.DeepEqual(service.counted.System, []string{"unchanged"}) {
			t.Fatalf("count model=%q: %+v, %v", model, service.counted, err)
		}
	}
	for _, model := range []string{antiTruncationModelPrefix, antiTruncationModelPrefix + antiTruncationModelPrefix + "test", antiTruncationModelPrefix + "models/test"} {
		if _, _, err := s.resolveAntiTruncationModel(model); err == nil {
			t.Fatalf("invalid prefix accepted: %q", model)
		}
	}
	s.config.AntiTruncation = nil
	if _, err := s.countTokens(t.Context(), aistudio.TokenCountRequest{Model: antiTruncationModelPrefix + "test"}); err == nil {
		t.Fatal("disabled prefix accepted by token counting")
	}
}

func TestAntiTruncationTokenCountEndpoints(t *testing.T) {
	service := &antiTruncationTestService{}
	var enabled atomic.Bool
	handler := NewHandler(service, Config{AntiTruncation: enabled.Load})
	for _, on := range []bool{true, false} {
		enabled.Store(on)
		for _, protocol := range []string{"messages", "responses", "gemini"} {
			path, body := antiTruncationProtocolRequest(protocol, antiTruncationModelPrefix+"test", false)
			switch protocol {
			case "messages":
				path += "/count_tokens"
			case "responses":
				path += "/input_tokens"
			case "gemini":
				path = strings.Replace(path, ":generateContent", ":countTokens", 1)
			}
			service.counted = aistudio.TokenCountRequest{}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			if !on {
				if w.Code != http.StatusNotFound || service.counted.Model != "" {
					t.Fatalf("%s accepted disabled prefix: status=%d: %s", protocol, w.Code, w.Body)
				}
				continue
			}
			if w.Code != http.StatusOK || service.counted.Model != "test" || len(service.counted.Tools.Functions) != 0 {
				t.Fatalf("%s count changed: request=%+v, status=%d: %s", protocol, service.counted, w.Code, w.Body)
			}
		}
	}
}

type antiTruncationFixtureTransport struct {
	protocolFixtureTransport
	duringRequest func()
}

func (transport *antiTruncationFixtureTransport) DoProtected(_ context.Context, request aistudio.GenerateRequest, _ aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	transport.requests <- request
	if transport.duringRequest != nil {
		transport.duringRequest()
	}
	if len(request.Tools.Functions) == 0 {
		return nil, fmt.Errorf("anti-truncation transport tool was not injected")
	}
	name := request.Tools.Functions[len(request.Tools.Functions)-1].Name
	call := make([]any, 15)
	arguments := []any{[]any{[]any{"content", []any{nil, nil, "complete answer\n\"quoted\" \\ path"}}}}
	call[10], call[14] = []any{name, arguments, "internal-call"}, "internal-signature"
	content := []any{[]any{[]any{nil, "discard this preamble"}, call}, "model"}
	frame := []any{[]any{[]any{content, 1}}, nil, []any{10, 5, 15}}
	output, err := json.Marshal([]any{[]any{frame}})
	if err != nil {
		return nil, err
	}
	return &aistudio.RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {aistudio.JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(output))}, nil
}

func TestAntiTruncationProtocolOutput(t *testing.T) {
	row := make([]any, 65)
	row[0], row[3], row[5], row[6] = "models/test", "Test", 32768, 4096
	row[7], row[64] = []string{"generateContent"}, []int{1, 10, 25, 35}
	catalog, err := json.Marshal([]any{[]any{row}})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"chat", "messages", "responses", "gemini", "interactions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				var enabled atomic.Bool
				enabled.Store(true)
				transport := &antiTruncationFixtureTransport{
					protocolFixtureTransport: protocolFixtureTransport{catalog: catalog, requests: make(chan aistudio.GenerateRequest, 1)},
					// Disabling while the request is in flight must not expose its transport tool.
					duringRequest: func() { enabled.Store(false) },
				}
				client := aistudio.NewClient(aistudio.ClientOptions{Transport: transport, Protected: transport, Pages: transport})
				s := httptest.NewServer(NewHandler(generationTestService{generate: client.Generate}, Config{AntiTruncation: enabled.Load}))
				s.Client().Timeout = 5 * time.Second
				defer s.Close()
				path, body := antiTruncationProtocolRequest(protocol, antiTruncationModelPrefix+"test", stream)
				response, raw := protocolTestRequest(t, s, http.MethodPost, path, body, nil)
				if response.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte("complete answer")) {
					t.Fatalf("status=%d: %s", response.StatusCode, raw)
				}
				request := <-transport.requests
				if request.Model != "test" || !request.AntiTruncation || !strings.HasPrefix(request.Tools.Functions[0].Name, "agw_emit_") {
					t.Fatalf("unexpected upstream request: %+v", request)
				}
				for _, hidden := range []string{"discard this preamble", "agw_emit_", "internal-call", "internal-signature", "anti_truncation_tool_calls", "AntiTruncationToolCalls"} {
					if bytes.Contains(raw, []byte(hidden)) {
						t.Fatalf("leaked %q: %s", hidden, raw)
					}
				}
				assertAntiTruncationResponseModel(t, raw, stream, "test")
			})
		}
	}
}

func assertAntiTruncationResponseModel(t *testing.T, raw []byte, stream bool, want string) {
	t.Helper()
	var frames []map[string]any
	if stream {
		frames = sseTestPayloads(t, raw)
	} else {
		var frame map[string]any
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	found := 0
	var inspect func(map[string]any)
	inspect = func(frame map[string]any) {
		for _, key := range []string{"model", "modelVersion"} {
			if value, ok := frame[key].(string); ok {
				found++
				if value != want {
					t.Errorf("response %s=%q, want %q", key, value, want)
				}
			}
		}
		for _, key := range []string{"response", "message", "interaction"} {
			if child, ok := frame[key].(map[string]any); ok {
				inspect(child)
			}
		}
	}
	for _, frame := range frames {
		inspect(frame)
	}
	if found == 0 {
		t.Fatalf("response has no model field: %s", raw)
	}
}

func TestAntiTruncationResponseModelAndAudit(t *testing.T) {
	for _, protocol := range []string{"chat", "messages", "responses", "gemini", "interactions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				admin := &modelLogAdmin{}
				handler := NewHandler(modelLogService{}, Config{Admin: admin, AntiTruncation: func() bool { return true }})
				requested := antiTruncationModelPrefix + "test"
				path, body := antiTruncationProtocolRequest(protocol, requested, stream)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
				if w.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", w.Code, w.Body)
				}
				want := "test"
				loggedResponse := want
				if protocol == "gemini" {
					want, loggedResponse = "provider-model", ""
				}
				assertAntiTruncationResponseModel(t, w.Body.Bytes(), stream, want)
				if admin.entry.RequestedModel != requested || admin.entry.ResponseModel != loggedResponse || admin.entry.Model != "provider-model" {
					t.Fatalf("request audit, emitted model and provider model were conflated: %+v", admin.entry)
				}
			})
		}
	}
}

func TestBaseResponseModel(t *testing.T) {
	for _, test := range []struct{ model, want string }{
		{"test", "test"},
		{"models/test", "models/test"},
		{antiTruncationModelPrefix + "mapped-search(high)", "mapped-search(high)"},
		{"models/" + antiTruncationModelPrefix + "test", "test"},
		{" mapped-" + antiTruncationModelPrefix + "test ", " mapped-" + antiTruncationModelPrefix + "test "},
	} {
		if got := baseResponseModel(test.model); got != test.want {
			t.Errorf("model=%q: got %q, want %q", test.model, got, test.want)
		}
	}
	request := responsesRequest{Model: antiTruncationModelPrefix + "test"}
	for _, status := range []string{"queued", "in_progress", "completed", "incomplete", "failed", "cancelled"} {
		if got := responseShell("response", 1, status, request)["model"]; got != "test" {
			t.Errorf("%s model=%v", status, got)
		}
	}
	if request.Model != antiTruncationModelPrefix+"test" {
		t.Fatal("response projection changed the model used for routing and history")
	}
}
