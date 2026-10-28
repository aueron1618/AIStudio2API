package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type generationTestService struct {
	aistudio.Service
	generate func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error)
}

func (s generationTestService) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	return s.generate(ctx, request)
}

func testEventStream(events ...aistudio.Event) <-chan aistudio.Event {
	stream := make(chan aistudio.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

func newProtocolTestServer(t *testing.T, service aistudio.Service) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(NewHandler(service, Config{}))
	s.Client().Timeout = 5 * time.Second
	t.Cleanup(s.Close)
	return s
}

func protocolTestRequest(t *testing.T, s *httptest.Server, method, path, body string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		r.Header[name] = values
	}
	response, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, raw
}

func sseTestPayloads(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var frames []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			t.Fatalf("invalid SSE data %q: %v", data, err)
		}
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		t.Fatalf("no SSE frames: %s", raw)
	}
	return frames
}

type protocolFixtureTransport struct {
	catalog, output []byte
	requests        chan aistudio.GenerateRequest
}

func (transport *protocolFixtureTransport) Do(_ context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	if request.Method != "ListModels" {
		return nil, fmt.Errorf("unexpected RPC %s", request.Method)
	}
	return &aistudio.RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {aistudio.JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(transport.catalog))}, nil
}

func (transport *protocolFixtureTransport) DoProtected(_ context.Context, request aistudio.GenerateRequest, _ aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	transport.requests <- request
	return &aistudio.RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {aistudio.JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(transport.output))}, nil
}

func TestExplicitThinkingOutput(t *testing.T) {
	// Exercise the real client filter, rather than imitating HideThinking in a service fake.
	row := make([]any, 65)
	row[0], row[3], row[5], row[6] = "models/test", "Test", 32768, 4096
	row[7], row[64] = []string{"generateContent"}, []int{1, 10, 25, 35}
	catalog, err := json.Marshal([]any{[]any{row}})
	if err != nil {
		t.Fatal(err)
	}
	thought, call := make([]any, 15), make([]any, 15)
	thought[1], thought[12], thought[14] = "private-thought", true, "fixture-signature"
	call[10], call[14] = []any{"lookup", nil, "call_sig"}, "fixture-signature"
	content := []any{[]any{thought, call, []any{nil, "answer"}}, "model"}
	frame := []any{[]any{[]any{content, 1}}, nil, []any{10, 2, 15, nil, nil, nil, nil, 0, nil, 3}}
	output, err := json.Marshal([]any{[]any{frame}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		protocol, name, config string
		hidden                 bool
	}{
		{"gemini", "default", "", true},
		{"gemini", "level_only", `,"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}`, true},
		{"gemini", "false", `,"generationConfig":{"thinkingConfig":{"includeThoughts":false}}`, true},
		{"gemini", "true", `,"generationConfig":{"thinkingConfig":{"includeThoughts":true}}`, false},
		{"gemini", "snake_case", `,"generation_config":{"thinking_config":{"include_thoughts":true}}`, false},
		{"anthropic", "default", "", true},
		{"anthropic", "disabled", `,"thinking":{"type":"disabled"}`, true},
		{"anthropic", "enabled", `,"thinking":{"type":"enabled","budget_tokens":1024}`, false},
		{"anthropic", "adaptive", `,"thinking":{"type":"adaptive"}`, false},
		{"responses", "default", "", true},
		{"responses", "effort_only", `,"reasoning":{"effort":"high"}`, true},
		{"responses", "none", `,"reasoning":{"summary":"none"}`, true},
		{"responses", "auto", `,"reasoning":{"summary":"auto"}`, false},
		{"responses", "concise", `,"reasoning":{"summary":"concise"}`, false},
		{"responses", "detailed", `,"reasoning":{"summary":"detailed"}`, false},
		{"responses", "legacy_summary", `,"reasoning":{"generate_summary":"auto"}`, false},
		{"responses", "summary_precedence", `,"reasoning":{"summary":"none","generate_summary":"auto"}`, true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/stream=%t", tc.protocol, tc.name, stream), func(t *testing.T) {
				transport := &protocolFixtureTransport{catalog: catalog, output: output, requests: make(chan aistudio.GenerateRequest, 1)}
				client, err := aistudio.NewClient(aistudio.ClientOptions{Transport: transport, Protected: transport})
				if err != nil {
					t.Fatal(err)
				}
				path, body, usage := "", "", ""
				switch tc.protocol {
				case "gemini":
					path = "/v1beta/models/test:generateContent"
					if stream {
						path = "/v1beta/models/test:streamGenerateContent?alt=sse"
					}
					body = `{"contents":[{"parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"}]}]` + tc.config + `}`
					usage = `"thoughtsTokenCount":3`
				case "anthropic":
					path = "/v1/messages"
					body = `{"model":"test","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup"}]` + tc.config + fmt.Sprintf(`,"stream":%t}`, stream)
					usage = `"output_tokens":5`
				case "responses":
					path = "/v1/responses"
					// Responses returns replayable signatures through its standard opt-in field.
					body = `{"model":"test","input":"hi","include":["reasoning.encrypted_content"],"tools":[{"type":"function","name":"lookup"}]` + tc.config + fmt.Sprintf(`,"stream":%t}`, stream)
					usage = `"reasoning_tokens":3`
				}
				response, raw := protocolTestRequest(t, newProtocolTestServer(t, client), http.MethodPost, path, body, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status=%d: %s", response.StatusCode, raw)
				}
				select {
				case request := <-transport.requests:
					if request.Config.HideThinking != tc.hidden {
						t.Errorf("HideThinking=%t, want %t", request.Config.HideThinking, tc.hidden)
					}
				default:
					t.Fatal("no GenerateContent request")
				}
				if bytes.Contains(raw, []byte("private-thought")) == tc.hidden {
					t.Errorf("hidden=%t: %s", tc.hidden, raw)
				}
				if !bytes.Contains(raw, []byte("fixture-signature")) || !bytes.Contains(raw, []byte(usage)) {
					t.Errorf("signature or thinking usage lost: %s", raw)
				}
				if stream {
					_ = sseTestPayloads(t, raw)
				}
			})
		}
	}
}

func TestChatLegacyFunctions(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			requests := make(chan aistudio.GenerateRequest, 1)
			call := aistudio.FunctionCall{ID: "call_new", Name: "lookup", Arguments: json.RawMessage(`{"query":"now"}`)}
			service := generationTestService{generate: func(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
				requests <- request
				return testEventStream(aistudio.Event{Kind: aistudio.EventToolCall, ToolCall: &call}, aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "other"}), nil
			}}
			body := `{"model":"test","functions":[{"name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}],"function_call":{"name":"lookup"},"parallel_tool_calls":true,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"function_call":{"name":"lookup","arguments":"{\"query\":\"before\"}"}},{"role":"function","name":"lookup","content":"old result"}]` + fmt.Sprintf(`,"stream":%t}`, stream)
			response, raw := protocolTestRequest(t, newProtocolTestServer(t, service), http.MethodPost, "/v1/chat/completions", body, nil)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status=%d: %s", response.StatusCode, raw)
			}
			request := <-requests
			config := request.Tools.ToolConfig
			if len(request.Tools.Functions) != 1 || request.Tools.Functions[0].Name != "lookup" || config.Mode != "required" || !reflect.DeepEqual(config.AllowedFunctionNames, []string{"lookup"}) || config.ParallelCalls == nil || *config.ParallelCalls {
				t.Fatalf("legacy tool config=%+v", request.Tools)
			}
			if len(request.Contents) != 3 || request.Contents[1].Role != aistudio.RoleAssistant || request.Contents[2].Role != aistudio.RoleTool {
				t.Fatalf("legacy history=%+v", request.Contents)
			}
			historyCall, historyResult := request.Contents[1].Parts[0].FunctionCall, request.Contents[2].Parts[0].FunctionResult
			if historyCall == nil || historyCall.Name != "lookup" || string(historyCall.Arguments) != `{"query":"before"}` || historyResult == nil || historyResult.Name != "lookup" || string(historyResult.Content) != `{"result":"old result"}` {
				t.Fatalf("call=%+v result=%+v", historyCall, historyResult)
			}
			if _, err := aistudio.EncodeGenerateContentRequest(request, aistudio.GenerationDefaults{MaxOutputTokens: 1024}, aistudio.RequestContext{}); err != nil {
				t.Fatalf("legacy history cannot be encoded: %v", err)
			}
			var frames []map[string]any
			if stream {
				frames = sseTestPayloads(t, raw)
				if strings.Count(string(raw), "data: [DONE]") != 1 {
					t.Fatalf("missing stream terminator: %s", raw)
				}
			} else {
				var frame map[string]any
				if err := json.Unmarshal(raw, &frame); err != nil {
					t.Fatal(err)
				}
				frames = []map[string]any{frame}
			}
			calls, finishes := 0, 0
			for _, frame := range frames {
				choice := frame["choices"].([]any)[0].(map[string]any)
				key := "message"
				if stream {
					key = "delta"
				}
				message := choice[key].(map[string]any)
				if message["tool_calls"] != nil {
					t.Errorf("legacy request returned tool_calls: %s", raw)
				}
				if output, ok := message["function_call"].(map[string]any); ok {
					calls++
					if !reflect.DeepEqual(output, map[string]any{"name": call.Name, "arguments": string(call.Arguments)}) {
						t.Errorf("legacy call=%+v", output)
					}
				}
				if choice["finish_reason"] == "function_call" {
					finishes++
				}
			}
			if calls != 1 || finishes != 1 {
				t.Fatalf("calls=%d finishes=%d: %s", calls, finishes, raw)
			}
		})
	}
	for _, tc := range []struct{ choice, modern, mode string }{
		{`"auto"`, "", "auto"}, {`"none"`, "", "none"}, {`{"name":"lookup"}`, `"none"`, "none"},
	} {
		request := chatRequest{Functions: []json.RawMessage{json.RawMessage(`{"name":"lookup"}`)}, FunctionCall: json.RawMessage(tc.choice), ToolChoice: json.RawMessage(tc.modern)}
		if err := request.mergeLegacyFunctions(); err != nil {
			t.Fatal(err)
		}
		generated, err := request.toGenerateRequest("test")
		if err != nil || generated.Tools.ToolConfig.Mode != tc.mode {
			t.Errorf("choice=%s modern=%s config=%+v err=%v", tc.choice, tc.modern, generated.Tools.ToolConfig, err)
		}
	}
	request := chatRequest{Functions: []json.RawMessage{json.RawMessage(`42`)}}
	if request.mergeLegacyFunctions() == nil {
		t.Error("invalid legacy declaration accepted")
	}
}

func TestProtocolFinishReasons(t *testing.T) {
	for _, tc := range []struct {
		reasons, chat, anthropic, incomplete string
	}{
		{"stop,unspecified,other,language,malformed_function_call,unexpected_tool_call,too_many_tool_calls,image_other,no_image,missing_thought_signature,unrecognized_name", "stop", "end_turn", ""},
		{"safety,content_filter,blocked,recitation,blocklist,prohibited_content,spii,image_safety,image_prohibited_content,image_recitation,provider_42", "content_filter", "refusal", "content_filter"},
		{"max_tokens,max_output_tokens,length", "length", "max_tokens", "max_output_tokens"},
		{"stop_sequence", "stop", "stop_sequence", ""},
		{"pause_turn", "stop", "pause_turn", ""},
	} {
		for _, reason := range strings.Split(tc.reasons, ",") {
			for _, hasTools := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/tools=%t", reason, hasTools), func(t *testing.T) {
					result := generationResult{finishReason: " " + strings.ToUpper(reason) + " ", stopSequence: "END"}
					if hasTools {
						result.toolCalls = []aistudio.FunctionCall{{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{}`)}}
					}
					chat, anthropic := tc.chat, tc.anthropic
					if hasTools && chat == "stop" && reason != "stop_sequence" {
						chat = "tool_calls"
					}
					if hasTools && anthropic == "end_turn" {
						anthropic = "tool_use"
					}
					if got := chatCompletionChoice(0, result, nil, mediaOutput{})["finish_reason"]; got != chat {
						t.Errorf("chat=%v want=%s", got, chat)
					}
					message := buildAnthropicResponse("test", "test", result, "", false, mediaOutput{})
					if message["stop_reason"] != anthropic {
						t.Errorf("anthropic=%v want=%s", message["stop_reason"], anthropic)
					}
					if reason == "stop_sequence" && *message["stop_sequence"].(*string) != "END" {
						t.Error("stop sequence was lost")
					}
					response, err := buildResponsesObject("test", 0, responsesRequest{}, result, nil)
					if err != nil {
						t.Fatal(err)
					}
					if tc.incomplete == "" {
						if response["status"] != "completed" || response["incomplete_details"] != nil {
							t.Errorf("normal finish marked incomplete: %+v", response)
						}
					} else if response["status"] != "incomplete" || !reflect.DeepEqual(response["incomplete_details"], map[string]any{"reason": tc.incomplete}) {
						t.Errorf("incomplete finish=%+v", response)
					}
					if reason == "provider_42" && (response["provider_finish_reason"] != reason || message["provider_finish_reason"] != reason) {
						t.Error("provider finish reason lost")
					}
				})
			}
		}
	}
}
