package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRequestControlsAreIndependentAndPreserveToolPayloads(t *testing.T) {
	entry := modelEntry{defaults: GenerationDefaults{ThinkingLevel: true, DefaultThinkingLevel: 3}}
	request := GenerateRequest{
		Model: "fixture", System: []string{"original"},
		Contents: []Content{
			{Role: RoleUser, Parts: []Part{{Text: "\u6c49\u8bed"}, {Text: "\u6c49\u8bed", Thought: true}, {FunctionResult: &FunctionResult{Name: "final", Content: json.RawMessage(`{"content":"\u6c49\u8bed"}`)}}}},
			{Role: RoleAssistant, Parts: []Part{{Text: "\u6c49\u8bed"}, {FunctionCall: &FunctionCall{Name: "final", Arguments: json.RawMessage(`{"content":"\u6c49\u8bed"}`)}}}},
		},
		Tools:  Tools{Functions: []FunctionDeclaration{{Name: "final", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		Config: GenerationConfig{ReasoningEffort: "high"},
	}
	if thinkingGuardEligible(request, entry) || chineseRewriteEligible(request, entry) {
		t.Fatal("new controls were enabled by default")
	}
	before, _ := json.Marshal(request)
	request.ThinkingGuard, request.UserMessageRewrite, request.AntiTruncation = true, userMessageRewriteTraditional, true
	rewritten, err := applyChineseRewrite(request)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Contents[0].Parts[0].Text != "\u6f22\u8a9e" || !reflect.DeepEqual(rewritten.Contents[0].Parts[1:], request.Contents[0].Parts[1:]) || !reflect.DeepEqual(rewritten.Contents[1], request.Contents[1]) || !reflect.DeepEqual(rewritten.Tools, request.Tools) {
		t.Fatal("rewrite changed thought/history/tool payloads or failed to convert user text")
	}
	guarded := applyThinkingGuard(rewritten, entry)
	if guarded.Config.ReasoningEffort != "low" || !strings.Contains(SystemText(guarded.System), thinkingGuardInstruction) || !strings.Contains(SystemText(guarded.System), userMessageRewriteReplyInstruction) {
		t.Fatal("guard and rewrite failed to coexist as separate controls")
	}
	prepared, transport, err := prepareAntiTruncation(guarded)
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.ThinkingGuard || prepared.UserMessageRewrite != userMessageRewriteTraditional || !strings.HasPrefix(transport.name, "agw_emit_") || prepared.Tools.Functions[0].Name != "final" || !prepared.antiTruncationControlTail || !strings.Contains(prepared.Contents[len(prepared.Contents)-1].Parts[0].Text, transport.name) {
		t.Fatal("anti-truncation replaced a control or a genuine client final tool")
	}
	request.ThinkingGuard, request.UserMessageRewrite, request.AntiTruncation = false, "", false
	after, _ := json.Marshal(request)
	if !bytes.Equal(before, after) {
		t.Fatal("controls modified the original request backing arrays")
	}
	for _, effort := range []string{"none", "minimal", "low", "invalid"} {
		request.Config.ReasoningEffort = effort
		if got := applyThinkingGuard(request, entry).Config.ReasoningEffort; got != effort {
			t.Fatalf("guard changed lower or invalid effort %q to %q", effort, got)
		}
	}
	for _, capability := range []string{"image_route", "speech_route", "video_route", "music_route", "transcription_output"} {
		entry.model.Capabilities = map[string]bool{capability: true}
		request.ThinkingGuard, request.UserMessageRewrite = true, userMessageRewriteTraditional
		if thinkingGuardEligible(request, entry) || chineseRewriteEligible(request, entry) {
			t.Fatalf("text controls admitted non-text capability %q", capability)
		}
	}
}

func TestClientFinalToolIsNotConvertedToTextOrStoppedInsideArguments(t *testing.T) {
	arguments := json.RawMessage(`{"content":"answer<stop>still tool data"}`)
	client := NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
		if len(request.Tools.Functions) != 1 || request.Tools.Functions[0].Name != "final" {
			t.Fatal("ordinary client tool declarations were injected or replaced")
		}
		return antiTruncationTestResponse(t, []Part{{FunctionCall: &FunctionCall{Name: "final", Arguments: arguments}}}, 1), nil
	})})
	model := Model{ID: "fixture", Capabilities: map[string]bool{"function_declarations": true}}
	client.setModelCatalog("", modelCatalog{entries: map[string]modelEntry{model.ID: {model: model, defaults: GenerationDefaults{MaxOutputTokens: 1024}}}})
	events, err := client.Generate(t.Context(), GenerateRequest{
		Model: model.ID, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "question"}}}},
		Tools:  Tools{Functions: []FunctionDeclaration{{Name: "final", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		Config: GenerationConfig{StopSequences: []string{"<stop>"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, finishes, usages := 0, 0, 0
	for event := range events {
		switch event.Kind {
		case EventToolCall:
			calls++
			var got map[string]string
			if err := json.Unmarshal(event.FunctionCall.Arguments, &got); err != nil || event.FunctionCall.Name != "final" || len(got) != 1 || got["content"] != "answer<stop>still tool data" {
				t.Fatalf("client final arguments were converted or stop-filtered: %s %s (%v)", event.FunctionCall.Name, event.FunctionCall.Arguments, err)
			}
		case EventText:
			t.Fatal("a client final function was unwrapped as reply text")
		case EventFinish:
			finishes++
		case EventUsage:
			usages++
			if event.Usage.InputTokens != 10 || event.Usage.OutputTokens != 4 || event.Usage.TotalTokens != 14 {
				t.Fatalf("genuine tool usage changed: %+v", event.Usage)
			}
		case EventError:
			t.Fatal(event.Err)
		}
	}
	if calls != 1 || finishes != 1 || usages != 1 {
		t.Fatalf("ordinary final lost tool/finish/usage events: %d/%d/%d", calls, finishes, usages)
	}
}
