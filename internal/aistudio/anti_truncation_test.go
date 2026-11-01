package aistudio

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const antiTruncationTestName = "agw_emit_0123456789abcdef01234567"

func antiTruncationTestCall(name, text string) Event {
	arguments, _ := json.Marshal(map[string]string{"content": text})
	return Event{Kind: EventToolCall, Part: Part{FunctionCall: &FunctionCall{
		ID: "internal-call", Name: name, Arguments: arguments, ThoughtSignature: "internal-signature",
	}, ThoughtSignature: "internal-signature"}}
}

func antiTruncationTestEvents(transport *antiTruncationTransport, source []Event, decodeErr error) ([]Event, error) {
	var events []Event
	emit := transport.emitter(func(event Event) error {
		events = append(events, event)
		return nil
	})
	for _, event := range source {
		if err := emit(event); err != nil {
			return events, transport.complete(err)
		}
	}
	return events, transport.complete(decodeErr)
}

func TestPrepareAntiTruncation(t *testing.T) {
	parallel := false
	for _, tail := range []Role{RoleUser, RoleAssistant, RoleTool} {
		t.Run(string(tail), func(t *testing.T) {
			functions := []FunctionDeclaration{
				{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`), Strict: true},
				{Name: "agw_emit_previous_request", Parameters: json.RawMessage(`{"type":"object"}`)},
				{Name: "unused-capacity"},
			}
			system := []string{"original system", "unused system capacity"}
			contents := []Content{
				{Role: RoleAssistant, Parts: []Part{{FunctionCall: &FunctionCall{ID: "real", Name: "lookup", Arguments: json.RawMessage(`{}`), ThoughtSignature: "real-signature"}}}},
				{Role: tail, Parts: []Part{{Text: "original tail"}}},
				{Role: RoleUser, Parts: []Part{{Text: "unused content capacity"}}},
			}
			request := GenerateRequest{
				Model: "any-model", AntiTruncation: true, System: system[:1], Contents: contents[:2],
				Tools: Tools{Functions: functions[:2], Google: []string{"google_search"}, ToolConfig: ToolConfig{
					Mode: "required", AllowedFunctionNames: []string{"lookup"}, ParallelCalls: &parallel,
					ServerSideInvocations: true, Retrieval: &RetrievalConfig{LanguageCode: "en"},
				}},
			}
			before, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for range 16 {
				prepared, transport, err := prepareAntiTruncation(request)
				if err != nil {
					t.Fatal(err)
				}
				if transport.calls != 0 {
					t.Fatal("preparation counted an unobserved tool call")
				}
				if !strings.HasPrefix(transport.name, "agw_emit_") || seen[transport.name] {
					t.Fatalf("tool name was not unique: %q", transport.name)
				}
				nonce, err := hex.DecodeString(strings.TrimPrefix(transport.name, "agw_emit_"))
				if err != nil || len(nonce) != 12 || transport.name != strings.ToLower(transport.name) {
					t.Fatalf("invalid 96-bit tool nonce: %q", transport.name)
				}
				seen[transport.name] = true
				if prepared.Model != request.Model || !reflect.DeepEqual(prepared.Tools.Functions[:2], request.Tools.Functions) {
					t.Fatal("model or genuine declarations changed")
				}
				declaration := prepared.Tools.Functions[2]
				if declaration.Name != transport.name || declaration.Description != "Use this transport tool exactly once for the final user-visible answer. Put the complete answer in content. Never wrap genuine tool calls in it." || string(declaration.Parameters) != `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}` {
					t.Fatalf("unexpected transport declaration: %+v", declaration)
				}
				config := prepared.Tools.ToolConfig
				if config.Mode != "auto" || config.AllowedFunctionNames != nil || config.ParallelCalls != nil || !config.ServerSideInvocations || config.Retrieval != request.Tools.ToolConfig.Retrieval {
					t.Fatalf("incorrect transport tool configuration: %+v", config)
				}
				instruction := "Always use tool `" + transport.name + "` to output your final reply in its `content` argument. Do not output anything outside this tool call."
				if tail == RoleAssistant {
					if len(prepared.Contents) != 3 || !reflect.DeepEqual(prepared.Contents[:2], request.Contents) || prepared.Contents[2].Role != RoleUser || !strings.HasSuffix(prepared.Contents[2].Parts[0].Text, instruction) || !reflect.DeepEqual(prepared.System, request.System) {
						t.Fatal("assistant tail was not preserved before the user control turn")
					}
				} else if !reflect.DeepEqual(prepared.Contents, request.Contents) || !strings.HasPrefix(SystemText(prepared.System), "original system\n") || !strings.HasSuffix(SystemText(prepared.System), instruction) {
					t.Fatal("control instruction was not appended to the system")
				}
			}
			after, err := json.Marshal(request)
			if err != nil || !bytes.Equal(before, after) || functions[2].Name != "unused-capacity" || system[1] != "unused system capacity" || contents[2].Parts[0].Text != "unused content capacity" {
				t.Fatal("preparation modified the original request or its backing arrays")
			}
		})
	}
	var decoded GenerateRequest
	if err := json.Unmarshal([]byte(`{"anti_truncation":true,"AntiTruncation":true}`), &decoded); err != nil || decoded.AntiTruncation {
		t.Fatalf("JSON input enabled the internal flag: %+v, %v", decoded, err)
	}
	encoded, err := json.Marshal(GenerateRequest{AntiTruncation: true})
	if err != nil || strings.Contains(strings.ToLower(string(encoded)), "truncation") {
		t.Fatalf("internal flag was serialized: %s, %v", encoded, err)
	}
}

func TestAntiTruncationContent(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
		invalid         bool
		incomplete      bool
	}{
		{name: "standard", raw: `{"content":"answer"}`, want: "answer"},
		{name: "empty", raw: `{"content":""}`},
		{name: "escapes", raw: `{"content":"a\n\"b\"\\c\u4e2d\ud83d\ude00"}`, want: "a\n\"b\"\\c\u4e2d\U0001f600"},
		{name: "fence", raw: "```json\n{\"content\":\"answer\"}\n```", want: "answer"},
		{name: "plain fence", raw: "```\r\n{\"content\":\"answer\"}\r\n```", want: "answer"},
		{name: "raw controls", raw: "{\"content\":\"a\nb\rc\td\"}", want: "a\nb\rc\td"},
		{name: "missing object end", raw: `{"content":"complete"`, want: "complete"},
		{name: "missing nested ends", raw: `{"content":"complete","extra":[{"x":1`, want: "complete"},
		{name: "braces inside text", raw: "{\"content\":\"{ [ } ] ```\"", want: "{ [ } ] ```"},
		{name: "top level only", raw: `{"tool":{"content":"wrong"},"content":"right"}`, want: "right"},
		{name: "unfinished content", raw: `{"content":"partial`, invalid: true, incomplete: true},
		{name: "unfinished escape", raw: `{"content":"partial\`, invalid: true, incomplete: true},
		{name: "unfinished unicode", raw: `{"content":"\u12`, invalid: true, incomplete: true},
		{name: "nested genuine tool", raw: `{"tools":[{"arguments":{"content":"wrong"}}]}`, invalid: true},
		{name: "nested repair", raw: `{"tools":[{"arguments":{"content":"wrong"}`, invalid: true},
		{name: "wrong case", raw: `{"Content":"wrong"}`, invalid: true},
		{name: "null content", raw: `{"content":null}`, invalid: true},
		{name: "object content", raw: `{"content":{"content":"wrong"}}`, invalid: true},
		{name: "array root", raw: `[{"content":"wrong"}]`, invalid: true},
		{name: "missing content", raw: `{}`, invalid: true},
		{name: "extra prose", raw: `answer: {"content":"wrong"}`, invalid: true},
		{name: "trailing prose", raw: `{"content":"wrong"} trailing`, invalid: true},
		{name: "mismatched nesting", raw: `{"content":"wrong","extra":[{]}`, invalid: true},
		{name: "extra brace", raw: `{"content":"wrong"}}`, invalid: true},
		{name: "unfinished fence", raw: "```json", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := antiTruncationContent(json.RawMessage(test.raw))
			if test.invalid {
				if err == nil || got != "" || test.incomplete && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("content=%q error=%v", got, err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("content=%q error=%v want=%q", got, err, test.want)
			}
		})
	}
	if _, err := antiTruncationContent(json.RawMessage(`{"content":"` + strings.Repeat("x", antiTruncationBufferLimit) + `"}`)); err == nil {
		t.Fatal("oversized arguments were accepted")
	}
}

func TestAntiTruncationReplacementAndMetadata(t *testing.T) {
	usage := &Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, Details: &UsageDetails{ServiceTier: "priority"}}
	reasoning := Event{Kind: EventReasoning, Part: Part{Text: "thinking", Thought: true, ThoughtSignature: "reasoning-signature"}}
	signature := Event{Kind: EventThoughtSignature, Part: Part{ThoughtSignature: "standalone-signature"}}
	real := antiTruncationTestCall("lookup", "genuine tool argument")
	first := antiTruncationTestCall(antiTruncationTestName, "answer one")
	first.ProviderModel, first.ResponseID, first.TurnToken = "provider-model", "response-id", "turn-token"
	first.FrameUsage, first.FinishFrame = usage, true
	first.DebugInfo, first.DetailedFinishReason = json.RawMessage(`{"debug":1}`), json.RawMessage(`{"detail":1}`)
	first.SafetyRatings = []SafetyRating{{Category: "safety", Probability: "LOW"}}
	second := antiTruncationTestCall(antiTruncationTestName, "answer two")
	grounding := Event{Kind: EventGrounding, Grounding: &GroundingMetadata{WebSearchQueries: []string{"query"}}}
	media := Event{Kind: EventMedia, Part: Part{InlineData: &Blob{MIME: "image/png", Data: []byte{1, 2, 3}}}}
	finish := Event{Kind: EventFinish, FinishReason: "TOOL_CALLS", FrameUsage: usage, FinishFrame: true}
	source := []Event{
		signature, {Kind: EventText, Part: Part{Text: "ordinary preamble"}}, reasoning, real,
		{Kind: EventUsage, Usage: usage}, first, {Kind: EventText, Part: Part{Text: "ordinary suffix"}},
		grounding, media, second, finish,
	}
	transport := &antiTruncationTransport{name: antiTruncationTestName}
	got, err := antiTruncationTestEvents(transport, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls=%d want=2", transport.calls)
	}
	unwrapped := first
	unwrapped.Kind, unwrapped.Part = EventText, Part{Text: "answer one"}
	want := []Event{signature, reasoning, real, {Kind: EventUsage, Usage: usage}, unwrapped, grounding, media, {Kind: EventText, Part: Part{Text: "answer two"}}, finish}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%+v\nwant=%+v", got, want)
	}
	if first.FunctionCall == nil || first.ThoughtSignature != "internal-signature" || first.FunctionCall.ThoughtSignature != "internal-signature" {
		t.Fatal("unwrapping mutated the source function call")
	}
}

func TestAntiTruncationFallbackAndFinishReasons(t *testing.T) {
	for _, reason := range []string{"STOP", "stop", "TOOL_CALLS", "tool_calls", "MAX_TOKENS", "max_tokens", "safety", "continuation"} {
		for _, synthetic := range []bool{false, true} {
			t.Run(reason+"/"+map[bool]string{false: "fallback", true: "synthetic"}[synthetic], func(t *testing.T) {
				transport := &antiTruncationTransport{name: antiTruncationTestName}
				var got []Event
				emit := transport.emitter(func(event Event) error { got = append(got, event); return nil })
				text := Event{Kind: EventText, Part: Part{Text: "ordinary"}, ResponseID: "original-response"}
				thought := Event{Kind: EventReasoning, Part: Part{Text: "thought", Thought: true}}
				for _, event := range []Event{text, thought} {
					if err := emit(event); err != nil {
						t.Fatal(err)
					}
				}
				if len(got) != 0 {
					t.Fatal("fallback text or later events escaped before the transport decision")
				}
				want := []Event{text, thought}
				if synthetic {
					if err := emit(antiTruncationTestCall(antiTruncationTestName, "")); err != nil {
						t.Fatal(err)
					}
					want = []Event{thought, {Kind: EventText}}
				}
				finish := Event{Kind: EventFinish, FinishReason: reason}
				if err := emit(finish); err != nil {
					t.Fatal(err)
				}
				if synthetic && strings.EqualFold(reason, "tool_calls") {
					finish.FinishReason = "STOP"
				}
				want = append(want, finish)
				if err := transport.complete(nil); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("events=%+v error=%v want=%+v", got, err, want)
				}
				if synthetic && transport.calls != 1 || !synthetic && transport.calls != 0 {
					t.Fatalf("synthetic=%t calls=%d", synthetic, transport.calls)
				}
			})
		}
	}
	other := antiTruncationTestCall(antiTruncationTestName+"_other", "not transport text")
	source := []Event{other, {Kind: EventFinish, FinishReason: "tool_calls"}}
	transport := &antiTruncationTransport{name: antiTruncationTestName}
	got, err := antiTruncationTestEvents(transport, source, nil)
	if err != nil || !reflect.DeepEqual(got, source) || transport.calls != 0 {
		t.Fatalf("a different random tool name was unwrapped: %+v, %v", got, err)
	}
}

func TestAntiTruncationErrorsAndIncompleteStreams(t *testing.T) {
	upstream := errors.New("upstream failed")
	for _, test := range []struct {
		name      string
		terminal  *Event
		decodeErr error
		wantErr   error
	}{
		{name: "missing finish", wantErr: io.ErrUnexpectedEOF},
		{name: "decoder failure", decodeErr: upstream, wantErr: upstream},
		{name: "error event", terminal: &Event{Kind: EventError, Err: upstream}, wantErr: upstream},
		{name: "unfinished arguments", terminal: &Event{Kind: EventToolCall, Part: Part{FunctionCall: &FunctionCall{Name: antiTruncationTestName, Arguments: json.RawMessage(`{"content":"partial`)}}}, wantErr: io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			thought := Event{Kind: EventReasoning, Part: Part{Text: "thought", Thought: true, ThoughtSignature: "keep"}}
			real := antiTruncationTestCall("lookup", "real")
			source := []Event{{Kind: EventText, Part: Part{Text: "must not escape"}}, thought, real}
			if test.terminal != nil {
				source = append(source, *test.terminal, Event{Kind: EventFinish, FinishReason: "STOP"})
			}
			transport := &antiTruncationTransport{name: antiTruncationTestName}
			got, err := antiTruncationTestEvents(transport, source, test.decodeErr)
			if !errors.Is(err, test.wantErr) || !reflect.DeepEqual(got, []Event{thought, real}) || len(transport.pending) != 0 {
				t.Fatalf("events=%+v error=%v", got, err)
			}
		})
	}
	got, err := antiTruncationTestEvents(&antiTruncationTransport{name: antiTruncationTestName}, []Event{antiTruncationTestCall(antiTruncationTestName, "answer")}, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(got) != 1 || got[0].Text != "answer" {
		t.Fatalf("a synthetic answer without a finish was accepted: %+v, %v", got, err)
	}
}

func TestAntiTruncationEmitterFailuresAndBounds(t *testing.T) {
	for _, failure := range []error{context.Canceled, io.ErrClosedPipe} {
		transport := &antiTruncationTransport{name: antiTruncationTestName}
		calls := 0
		emit := transport.emitter(func(Event) error { calls++; return failure })
		if err := emit(Event{Kind: EventText, Part: Part{Text: "buffered"}}); err != nil {
			t.Fatal(err)
		}
		if err := emit(Event{Kind: EventReasoning, Part: Part{Text: "thought"}}); err != nil {
			t.Fatal(err)
		}
		err := emit(antiTruncationTestCall(antiTruncationTestName, "answer"))
		if err != failure || transport.complete(err) != failure || emit(Event{Kind: EventFinish}) != failure || calls != 1 || transport.calls != 1 {
			t.Fatalf("emitter failure was retried or changed: calls=%d error=%v", calls, err)
		}
	}
	large := strings.Repeat("x", antiTruncationBufferLimit)
	for _, event := range []Event{
		{Kind: EventText, Part: Part{Text: large}},
		{Kind: EventReasoning, Part: Part{Text: large}},
		{Kind: EventUsage, Usage: &Usage{Details: &UsageDetails{ServiceTier: large}}},
		{Kind: EventReasoning, FrameUsage: &Usage{Details: &UsageDetails{ServiceTier: large}}},
	} {
		transport := &antiTruncationTransport{name: antiTruncationTestName}
		got, err := antiTruncationTestEvents(transport, []Event{
			{Kind: EventText, Part: Part{Text: "buffered"}},
			event,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "buffer exceeds") || len(got) != 0 || len(transport.pending) != 0 {
			t.Fatalf("unbounded %s buffer: events=%d error=%v", event.Kind, len(got), err)
		}
	}
	transport := &antiTruncationTransport{name: antiTruncationTestName}
	emit := transport.emitter(func(Event) error { t.Fatal("buffered event escaped"); return nil })
	if err := emit(Event{Kind: EventText}); err != nil {
		t.Fatal(err)
	}
	for range antiTruncationBufferLimit / 1024 {
		if err := emit(Event{Kind: EventUsage}); err != nil {
			return
		}
	}
	t.Fatal("empty non-text events grew the pending queue without a bound")
}

func TestAntiTruncationStopEmitter(t *testing.T) {
	for _, synthetic := range []bool{false, true} {
		transport := &antiTruncationTransport{name: antiTruncationTestName}
		matcher := newStopSequenceMatcher([]string{"<stop>"})
		matched := ""
		var output Output
		var got []Event
		emit := transport.emitter(stopSequenceEmitter(matcher, func(event Event) error {
			output.Add(event)
			got = append(got, event)
			return nil
		}, &matched))
		var source []Event
		if synthetic {
			source = append(source, Event{Kind: EventText, Part: Part{Text: "ignore <stop> preamble"}})
			for _, text := range []string{"answer<sto", "p>discard"} {
				source = append(source, antiTruncationTestCall(antiTruncationTestName, text))
			}
		} else {
			source = []Event{{Kind: EventText, Part: Part{Text: "answer<sto"}}, {Kind: EventText, Part: Part{Text: "p>discard"}}}
		}
		source = append(source, Event{Kind: EventFinish, FinishReason: "tool_calls"})
		var err error
		for _, event := range source {
			if err = emit(event); err != nil {
				break
			}
		}
		if !errors.Is(transport.complete(err), errStopSequenceMatched) || matched != "<stop>" || len(got) != 1 || got[0].Kind != EventText || got[0].Text != "answer" || !reflect.DeepEqual(output.Visible(), []Part{{Text: "answer"}}) {
			t.Fatalf("synthetic=%v stop=%q events=%+v error=%v", synthetic, matched, got, err)
		}
	}
}

func antiTruncationTestResponse(t *testing.T, parts []Part, finish any) *RPCResponse {
	t.Helper()
	content, err := encodeContent(Content{Role: RoleAssistant, Parts: parts})
	if err != nil {
		t.Fatal(err)
	}
	frame := []any{[]any{[]any{content, finish}}, nil, []any{10, 4, 14}}
	body, err := json.Marshal([]any{[]any{frame}})
	if err != nil {
		t.Fatal(err)
	}
	return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestAntiTruncationClientToolContracts(t *testing.T) {
	parallel := false
	for _, test := range []struct {
		name, mode  string
		parallel    *bool
		allowed     []string
		calls       []string
		unsupported bool
		invalidArgs bool
		wantError   bool
		wantCalls   int
		wantText    string
		wantTools   int
	}{
		{name: "required synthetic only", mode: "required", wantError: true, wantText: "answer", wantTools: 3},
		{name: "required genuine plus synthetic", mode: "required", calls: []string{"lookup"}, wantCalls: 1, wantText: "answer", wantTools: 3},
		{name: "parallel false excludes synthetic", parallel: &parallel, calls: []string{"lookup"}, wantCalls: 1, wantText: "answer", wantTools: 3},
		{name: "parallel false rejects two genuine", parallel: &parallel, calls: []string{"lookup", "lookup"}, wantError: true, wantCalls: 1, wantTools: 3},
		{name: "allowed genuine", allowed: []string{"lookup"}, calls: []string{"lookup"}, wantCalls: 1, wantText: "answer", wantTools: 2},
		{name: "unselected genuine", allowed: []string{"lookup"}, calls: []string{"other"}, wantError: true, wantTools: 2},
		{name: "validated genuine", mode: "validated", calls: []string{"lookup"}, invalidArgs: true, wantError: true, wantTools: 3},
		{name: "none transport only", mode: "none", wantText: "answer", wantTools: 1},
		{name: "none rejects genuine", mode: "none", calls: []string{"lookup"}, wantError: true, wantTools: 1},
		{name: "no function capability still injects", unsupported: true, wantText: "answer", wantTools: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sent GenerateRequest
			client := NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
				sent = request
				if len(request.Tools.Functions) != test.wantTools || request.Tools.ToolConfig.Mode != "auto" || request.Tools.ToolConfig.AllowedFunctionNames != nil || request.Tools.ToolConfig.ParallelCalls != nil {
					t.Fatalf("incorrect upstream tools: %+v", request.Tools)
				}
				name := request.Tools.Functions[len(request.Tools.Functions)-1].Name
				if !strings.HasPrefix(name, "agw_emit_") || !bytes.Contains(rpc.Body, []byte(name)) {
					t.Fatal("synthetic tool did not reach the encoded upstream request")
				}
				if test.mode == "none" && (len(request.Tools.Google) != 0 || request.Tools.GoogleSearch != nil || len(request.Tools.MCPServers) != 0) {
					t.Fatal("none enabled genuine server tools")
				}
				parts := []Part{{Text: "discarded preamble"}}
				for _, call := range test.calls {
					args := json.RawMessage(`{"query":"valid"}`)
					if test.invalidArgs {
						args = json.RawMessage(`{"query":123}`)
					}
					parts = append(parts, Part{FunctionCall: &FunctionCall{ID: "real-call", Name: call, Arguments: args, ThoughtSignature: "real-signature"}})
				}
				parts = append(parts, antiTruncationTestCall(name, "ans").Part, antiTruncationTestCall(name, "wer").Part)
				return antiTruncationTestResponse(t, parts, 1), nil
			})})
			model := Model{ID: "any-model", Capabilities: map[string]bool{"function_declarations": !test.unsupported, "google_search": !test.unsupported}}
			client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{model.ID: {model: model, defaults: GenerationDefaults{MaxOutputTokens: 1024}}}})
			request := GenerateRequest{
				Model: model.ID, AntiTruncation: true, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "question"}}}},
				Tools: Tools{Functions: []FunctionDeclaration{
					{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)},
					{Name: "other", Parameters: json.RawMessage(`{"type":"object"}`)},
				}, Google: []string{"google_search"}, ToolConfig: ToolConfig{Mode: test.mode, AllowedFunctionNames: test.allowed, ParallelCalls: test.parallel}},
			}
			events, err := client.Generate(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			calls, failures, finishes := 0, 0, 0
			internalCalls := 0
			for event := range events {
				internalCalls = max(internalCalls, event.AntiTruncationToolCalls)
				switch event.Kind {
				case EventText:
					text += event.Text
					if event.FunctionCall != nil || event.ThoughtSignature != "" {
						t.Fatal("internal call fields leaked into text")
					}
				case EventToolCall:
					calls++
					if event.FunctionCall.ThoughtSignature != "real-signature" {
						t.Fatal("genuine call signature changed")
					}
				case EventError:
					failures++
					if event.AntiTruncationToolCalls == 0 {
						t.Fatal("tool contract failure lost observed transport calls")
					}
				case EventFinish:
					finishes++
				}
			}
			if calls != test.wantCalls || text != test.wantText || (failures > 0) != test.wantError || test.wantError && finishes != 0 || !test.wantError && finishes != 1 {
				t.Fatalf("text=%q calls=%d errors=%d finishes=%d", text, calls, failures, finishes)
			}
			if (!test.wantError || len(test.calls) == 0) && internalCalls != 2 {
				t.Fatalf("transport calls=%d want=2", internalCalls)
			}
			if len(test.allowed) > 0 && (len(sent.Tools.Functions) != 2 || sent.Tools.Functions[0].Name != "lookup") {
				t.Fatal("allowed names did not filter genuine declarations before injection")
			}
		})
	}
}

func TestAntiTruncationClientIncompleteAndUnsupportedRoutes(t *testing.T) {
	for _, defaults := range []GenerationDefaults{{InteractionStream: true}, {InteractionTask: true}} {
		client := NewClient(ClientOptions{})
		client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{"model": {model: Model{ID: "model"}, defaults: defaults}}})
		events, err := client.Generate(t.Context(), GenerateRequest{Model: "model", AntiTruncation: true, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "question"}}}}})
		if !errors.Is(err, ErrInvalidArgument) || events != nil || !strings.Contains(err.Error(), "Interaction") {
			t.Fatalf("unsupported route silently bypassed transport: %v", err)
		}
	}
	client := NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
		return antiTruncationTestResponse(t, []Part{{Text: "unfinished ordinary answer"}}, nil), nil
	})})
	client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{"model": {model: Model{ID: "model"}, defaults: GenerationDefaults{MaxOutputTokens: 1024}}}})
	events, err := client.Generate(t.Context(), GenerateRequest{Model: "model", AntiTruncation: true, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "question"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 1 || got[0].Kind != EventError || got[0].Err == nil {
		t.Fatalf("unfinished stream leaked fallback text or a success event: %+v", got)
	}
}

func TestAntiTruncationPooledAssistantTailAndDisabledRequests(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		model := Model{ID: "model", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"function_declarations": true}}
		account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}}
		pool := NewAccountPool([]*Account{account}, 1)
		pool.SetUpstreamChannels([]Channel{ChannelPlayground})
		client := NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
			if enabled {
				if len(request.Contents) != 3 || request.Contents[1].Role != RoleAssistant || request.Contents[1].Parts[0].Text != "original assistant" || request.Contents[2].Role != RoleUser || strings.Contains(SystemText(request.System), "rewrite prompt") {
					t.Fatalf("pooled service rewrote the assistant history: %+v", request)
				}
				name := request.Tools.Functions[0].Name
				return antiTruncationTestResponse(t, []Part{antiTruncationTestCall(name, "answer").Part}, 1), nil
			}
			if len(request.Tools.Functions) != 0 || len(request.Contents) != 1 || !strings.Contains(SystemText(request.System), "rewrite prompt") || strings.Contains(SystemText(request.System), "agw_emit_") {
				t.Fatalf("ordinary request behavior changed: %+v", request)
			}
			return antiTruncationTestResponse(t, []Part{{Text: "ordinary"}, antiTruncationTestCall(antiTruncationTestName, "not unwrapped").Part}, 1), nil
		})})
		client.setModelCatalog(account.ID, modelCatalog{entries: map[string]modelEntry{model.ID: {model: model, defaults: GenerationDefaults{MaxOutputTokens: 1024}}}})
		service := NewPooledService(pool, client)
		service.ContinuationRewrite, service.ContinuationPrompt = true, "rewrite prompt"
		request := GenerateRequest{Model: model.ID, AccountID: account.ID, AntiTruncation: enabled, Contents: []Content{
			{Role: RoleUser, Parts: []Part{{Text: "question"}}}, {Role: RoleAssistant, Parts: []Part{{Text: "original assistant"}}},
		}}
		if err := service.ValidateGenerateRequest(request); err != nil {
			t.Fatal(err)
		}
		events, err := service.Generate(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		var got []Event
		for event := range events {
			if event.Kind == EventError {
				t.Fatal(event.Err)
			}
			got = append(got, event)
		}
		call := slices.ContainsFunc(got, func(event Event) bool { return event.Kind == EventToolCall })
		if call == enabled || len(got) == 0 || enabled && got[0].Text != "answer" || !enabled && got[0].Text != "ordinary" {
			t.Fatalf("enabled=%v events=%+v", enabled, got)
		}
	}
}

type antiTruncationCountTransport func(context.Context, RPCRequest) (*RPCResponse, error)

func (transport antiTruncationCountTransport) Do(ctx context.Context, request RPCRequest) (*RPCResponse, error) {
	return transport(ctx, request)
}

func TestAntiTruncationTruncationPreservesLatestTask(t *testing.T) {
	for _, test := range []struct {
		name                 string
		older, assistantTail bool
		latestTooLarge       bool
	}{
		{name: "oversized latest turn", assistantTail: true, latestTooLarge: true},
		{name: "oversized latest after older turn", older: true, assistantTail: true, latestTooLarge: true},
		{name: "drop older turn and keep prefill", older: true, assistantTail: true},
		{name: "real user tail stays a boundary", older: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := GenerateRequest{Model: "test", AntiTruncation: true, Truncate: true, System: []string{"original system"}}
			if test.older {
				request.Contents = append(request.Contents,
					Content{Role: RoleUser, Parts: []Part{{Text: "older task"}}},
					Content{Role: RoleAssistant, Parts: []Part{{Text: "older answer"}}})
			}
			latest := len(request.Contents)
			request.Contents = append(request.Contents, Content{Role: RoleUser, Parts: []Part{{Text: "latest task"}}})
			if test.assistantTail {
				request.Contents = append(request.Contents, Content{Role: RoleAssistant, Parts: []Part{{Text: "latest prefill"}}})
			}
			prepared, transport, err := prepareAntiTruncation(request)
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient(ClientOptions{Transport: antiTruncationCountTransport(func(_ context.Context, rpc RPCRequest) (*RPCResponse, error) {
				if rpc.Method != "CountTokens" || !bytes.Contains(rpc.Body, []byte(transport.name)) || !bytes.Contains(rpc.Body, []byte("original system")) {
					t.Fatalf("truncation omitted transport overhead: %s %s", rpc.Method, rpc.Body)
				}
				if !bytes.Contains(rpc.Body, []byte("latest task")) {
					t.Fatal("truncation considered keeping only the artificial control turn")
				}
				tokens := 50
				if test.latestTooLarge || bytes.Contains(rpc.Body, []byte("older task")) {
					tokens = 200
				}
				body, _ := json.Marshal([]int{tokens})
				return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})})
			client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{"test": {model: Model{ID: "test"}}}})
			got, err := client.truncateRequest(t.Context(), prepared, 100)
			if test.latestTooLarge {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("oversized latest task was not rejected: %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got.Contents, prepared.Contents[latest:]) {
				t.Fatalf("latest task/prefill/control changed: %+v, %v", got.Contents, err)
			}
		})
	}
}

func TestAntiTruncationClientCallSnapshots(t *testing.T) {
	call := func(text string) Part { return antiTruncationTestCall(antiTruncationTestName, text).Part }
	invalid := Part{FunctionCall: &FunctionCall{Name: antiTruncationTestName, Arguments: json.RawMessage(`{"content":123}`)}}
	for _, test := range []struct {
		name       string
		parts      []Part
		stop       bool
		unfinished bool
		wantCalls  []int
		wantText   string
		wantTools  int
		wantFinish string
		wantError  bool
	}{
		{name: "one call many events", parts: []Part{call("answer")}, wantCalls: []int{1, 1, 1}, wantText: "answer", wantFinish: "stop"},
		{name: "incrementing calls", parts: []Part{call("one"), call("two")}, wantCalls: []int{1, 2, 2, 2}, wantText: "onetwo", wantFinish: "stop"},
		{name: "buffered reasoning", parts: []Part{{Text: "discard"}, {Text: "thinking", Thought: true}, call("answer")}, wantCalls: []int{1, 1, 1, 1}, wantText: "answer", wantFinish: "stop"},
		{name: "ordinary fallback", parts: []Part{{Text: "fallback"}}, wantCalls: []int{0, 0, 0}, wantText: "fallback", wantFinish: "stop"},
		{name: "different random name", parts: []Part{antiTruncationTestCall(antiTruncationTestName+"_other", "not synthetic").Part}, wantCalls: []int{0, 0, 0}, wantTools: 1, wantFinish: "stop"},
		{name: "genuine plus synthetic", parts: []Part{antiTruncationTestCall("lookup", "real").Part, call("answer")}, wantCalls: []int{0, 1, 1, 1}, wantText: "answer", wantTools: 1, wantFinish: "stop"},
		{name: "empty content", parts: []Part{call("")}, wantCalls: []int{1, 1, 1}, wantFinish: "stop"},
		{name: "empty swallowed by matcher", parts: []Part{call("")}, stop: true, wantCalls: []int{1, 1}, wantFinish: "stop"},
		{name: "stop swallows whole answer", parts: []Part{call("<stop>")}, stop: true, wantCalls: []int{1, 1}, wantFinish: "stop_sequence"},
		{name: "stop spans empty calls", parts: []Part{call("<sto"), call("p>discard")}, stop: true, wantCalls: []int{2, 2}, wantFinish: "stop_sequence"},
		{name: "stop after visible content", parts: []Part{call("answer<sto"), call("p>discard")}, stop: true, wantCalls: []int{1, 2, 2}, wantText: "answer", wantFinish: "stop_sequence"},
		{name: "invalid first content", parts: []Part{invalid}, wantCalls: []int{1}, wantError: true},
		{name: "invalid later content", parts: []Part{call("answer"), invalid}, wantCalls: []int{1, 2}, wantText: "answer", wantError: true},
		{name: "unfinished synthetic stream", parts: []Part{call("answer")}, unfinished: true, wantCalls: []int{1, 1}, wantText: "answer", wantError: true},
		{name: "unfinished fallback stream", parts: []Part{{Text: "discard"}}, unfinished: true, wantCalls: []int{0}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient(ClientOptions{
				Transport: antiTruncationCountTransport(func(_ context.Context, request RPCRequest) (*RPCResponse, error) {
					if request.Method != "CountTokens" {
						return nil, errors.New("unexpected non-count RPC")
					}
					return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(strings.NewReader(`[10]`))}, nil
				}),
				Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
					parts := slices.Clone(test.parts)
					for index, part := range parts {
						if part.FunctionCall != nil && part.FunctionCall.Name == antiTruncationTestName {
							value := *part.FunctionCall
							value.Name = request.Tools.Functions[len(request.Tools.Functions)-1].Name
							parts[index].FunctionCall = &value
						}
					}
					var finish any = 1
					if test.unfinished {
						finish = nil
					}
					return antiTruncationTestResponse(t, parts, finish), nil
				}),
			})
			model := Model{ID: "model", Capabilities: map[string]bool{"function_declarations": true}}
			client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{model.ID: {model: model, defaults: GenerationDefaults{MaxOutputTokens: 1024}}}})
			request := GenerateRequest{Model: model.ID, AntiTruncation: true, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "question"}}}}}
			if test.stop {
				request.Config.StopSequences = []string{"<stop>"}
			}
			events, err := client.Generate(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			var counts []int
			text, finish := "", ""
			tools, failures := 0, 0
			for event := range events {
				counts = append(counts, event.AntiTruncationToolCalls)
				switch event.Kind {
				case EventText:
					text += event.Text
					if event.FunctionCall != nil {
						t.Fatal("transport call reappeared in text")
					}
				case EventToolCall:
					tools++
				case EventFinish:
					finish = event.FinishReason
				case EventError:
					failures++
					if event.Err == nil {
						t.Fatal("failure lost its error")
					}
				}
			}
			if !slices.Equal(counts, test.wantCalls) || text != test.wantText || tools != test.wantTools || finish != test.wantFinish || (failures > 0) != test.wantError {
				t.Fatalf("snapshots=%v text=%q genuine=%d finish=%q errors=%d", counts, text, tools, finish, failures)
			}
		})
	}
}

func TestAntiTruncationEventCounterJSON(t *testing.T) {
	event := Event{Kind: EventText, Part: Part{Text: "answer"}}
	before, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	event.AntiTruncationToolCalls = 3
	after, err := json.Marshal(event)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("internal counter changed JSON: %s, %v", after, err)
	}
	var decoded Event
	if err := json.Unmarshal([]byte(`{"kind":"text","AntiTruncationToolCalls":9,"anti_truncation_tool_calls":9}`), &decoded); err != nil || decoded.AntiTruncationToolCalls != 0 {
		t.Fatalf("JSON populated the internal counter: %+v, %v", decoded, err)
	}
}
