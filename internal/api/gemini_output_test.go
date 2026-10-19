package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGeminiOutputParts_MergeTextAndReasoning(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventReasoning, Text: "I think "},
			{Kind: aistudio.EventReasoning, Text: "therefore "},
			{Kind: aistudio.EventReasoning, Text: "I am."},
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_abc123"},
			{Kind: aistudio.EventText, Text: "Hello "},
			{Kind: aistudio.EventText, Text: "world!"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	// 合并相邻思考并挂载签名
	expectedThought := map[string]any{
		"thought":          true,
		"text":             "I think therefore I am.",
		"thoughtSignature": "sig_abc123",
	}
	if !reflect.DeepEqual(parts[0], expectedThought) {
		t.Errorf("parts[0] = %+v, want %+v", parts[0], expectedThought)
	}

	// 合并相邻正文
	expectedText := map[string]any{
		"text": "Hello world!",
	}
	if !reflect.DeepEqual(parts[1], expectedText) {
		t.Errorf("parts[1] = %+v, want %+v", parts[1], expectedText)
	}
}

func TestGeminiOutputParts_TextOnly(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventText, Text: "Chunk 1 "},
			{Kind: aistudio.EventText, Text: "Chunk 2 "},
			{Kind: aistudio.EventText, Text: "Chunk 3"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d: %+v", len(parts), parts)
	}

	expectedText := map[string]any{
		"text": "Chunk 1 Chunk 2 Chunk 3",
	}
	if !reflect.DeepEqual(parts[0], expectedText) {
		t.Errorf("parts[0] = %+v, want %+v", parts[0], expectedText)
	}
}

func TestGeminiOutputParts_LeadingThoughtSignature(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_pre"},
			{Kind: aistudio.EventReasoning, Text: "Thinking step."},
			{Kind: aistudio.EventText, Text: "Final text."},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	if parts[0]["thoughtSignature"] != "sig_pre" {
		t.Errorf("expected thoughtSignature 'sig_pre' on parts[0], got %v", parts[0]["thoughtSignature"])
	}
	if parts[0]["text"] != "Thinking step." {
		t.Errorf("expected text 'Thinking step.', got %v", parts[0]["text"])
	}
	if parts[1]["text"] != "Final text." {
		t.Errorf("expected text 'Final text.', got %v", parts[1]["text"])
	}
}

func TestGeminiOutputParts_StandaloneThoughtSignatureFallback(t *testing.T) {
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_only"},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d: %+v", len(parts), parts)
	}
	if parts[0]["thoughtSignature"] != "sig_only" {
		t.Errorf("expected thoughtSignature 'sig_only', got %v", parts[0]["thoughtSignature"])
	}
	if _, ok := parts[0]["thought"]; ok || parts[0]["text"] != "" {
		t.Errorf("expected empty text part without thought, got %+v", parts[0])
	}
}

func TestGeminiOutputParts_WithToolCall(t *testing.T) {
	call := aistudio.FunctionCall{
		ID:        "call_1",
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"location":"Tokyo"}`),
	}
	result := generationResult{
		events: []aistudio.Event{
			{Kind: aistudio.EventReasoning, Text: "Need weather for Tokyo."},
			{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig_call"},
			{Kind: aistudio.EventToolCall, ToolCall: &call},
		},
	}

	parts := geminiOutputParts(result)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}

	if parts[0]["thought"] != true || parts[0]["text"] != "Need weather for Tokyo." {
		t.Errorf("unexpected parts[0]: %+v", parts[0])
	}
	if parts[0]["thoughtSignature"] != "sig_call" {
		t.Errorf("expected sig_call on parts[0], got %v", parts[0]["thoughtSignature"])
	}
	if parts[1]["functionCall"] == nil {
		t.Errorf("expected functionCall on parts[1], got %+v", parts[1])
	}
}

// TestGeminiOutputParts_SignedBoundaries 保留相邻内容的签名边界
func TestGeminiOutputParts_SignedBoundaries(t *testing.T) {
	for _, kind := range []aistudio.EventKind{aistudio.EventText, aistudio.EventReasoning} {
		parts := geminiOutputParts(generationResult{events: []aistudio.Event{
			{Kind: kind, Text: "first", ThoughtSignature: "sig-a"},
			{Kind: kind, Text: "second", ThoughtSignature: "sig-b"},
		}})
		if len(parts) != 2 || parts[0]["text"] != "first" || parts[1]["text"] != "second" || parts[0]["thoughtSignature"] != "sig-a" || parts[1]["thoughtSignature"] != "sig-b" {
			t.Fatalf("kind=%s signed parts=%+v", kind, parts)
		}
	}
}

// TestGeminiOutputParts_PendingSignature 保留前置签名与已有签名
func TestGeminiOutputParts_PendingSignature(t *testing.T) {
	parts := geminiOutputParts(generationResult{events: []aistudio.Event{
		{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig-a"},
		{Kind: aistudio.EventReasoning, Text: "reason", ThoughtSignature: "sig-b"},
		{Kind: aistudio.EventText, Text: "answer"},
	}})
	want := []map[string]any{
		{"text": "", "thoughtSignature": "sig-a"},
		{"text": "reason", "thought": true, "thoughtSignature": "sig-b"},
		{"text": "answer"},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts=%+v want=%+v", parts, want)
	}
}

// TestGeminiOutputParts_TrailingSignatures 保留尾部签名与已有签名的顺序
func TestGeminiOutputParts_TrailingSignatures(t *testing.T) {
	parts := geminiOutputParts(generationResult{events: []aistudio.Event{
		{Kind: aistudio.EventText, Text: "answer", ThoughtSignature: "sig-a"},
		{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig-b"},
	}})
	want := []map[string]any{
		{"text": "answer", "thoughtSignature": "sig-a"},
		{"text": "", "thoughtSignature": "sig-b"},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts=%+v want=%+v", parts, want)
	}
}

// TestGeminiOutputParts_LeadingSignatures 保留连续前置签名的原始顺序
func TestGeminiOutputParts_LeadingSignatures(t *testing.T) {
	parts := geminiOutputParts(generationResult{events: []aistudio.Event{
		{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig-a"},
		{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig-b"},
		{Kind: aistudio.EventThoughtSignature, ThoughtSignature: "sig-c"},
		{Kind: aistudio.EventText, Text: "answer"},
	}})
	want := []map[string]any{
		{"text": "", "thoughtSignature": "sig-a"},
		{"text": "", "thoughtSignature": "sig-b"},
		{"text": "", "thoughtSignature": "sig-c"},
		{"text": "answer"},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts=%+v want=%+v", parts, want)
	}
}

// TestGeminiOutputParts_SignedAudio 保留 PCM 音频片段的独立签名
func TestGeminiOutputParts_SignedAudio(t *testing.T) {
	var result generationResult
	for _, sig := range []string{"audio-a", "audio-b"} {
		if err := result.apply(aistudio.Event{Kind: aistudio.EventMedia, ThoughtSignature: sig, Media: &aistudio.Media{MIME: "audio/l16;rate=24000", Data: []byte{1, 2}}}); err != nil {
			t.Fatal(err)
		}
	}
	parts := geminiOutputParts(result)
	if len(parts) != 2 || parts[0]["thoughtSignature"] != "audio-a" || parts[1]["thoughtSignature"] != "audio-b" {
		t.Fatalf("signed audio=%+v", parts)
	}
}

func TestGeminiCandidateEndHTTP(t *testing.T) {
	for _, sources := range []string{"grounding", "citations", "both"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", sources, stream), func(t *testing.T) {
				service := generationTestService{generate: func(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					var events []aistudio.Event
					// The second candidate has no visible content, but still has terminal metadata.
					if *request.Config.Seed == 7 {
						events = append(events, aistudio.Event{Kind: aistudio.EventText, Text: "Answer"})
					}
					if sources != "citations" {
						events = append(events, aistudio.Event{Kind: aistudio.EventGrounding, Grounding: &aistudio.GroundingMetadata{
							WebSearchQueries: []string{"query"}, Chunks: []aistudio.GroundingChunk{{Source: "web", URI: "https://search.test", Title: "Search"}},
						}})
					}
					if sources != "grounding" {
						for _, url := range []string{"https://citation.test/1", "https://citation.test/2"} {
							events = append(events, aistudio.Event{Kind: aistudio.EventCitation, Citation: &aistudio.Citation{URL: url, Title: "Native", Start: 1, End: 4}})
						}
					}
					events = append(events, aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 10, OutputTokens: 2, ReasoningTokens: 3, TotalTokens: 15}},
						aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop", FinishMessage: "finished", ProviderModel: "upstream-test"})
					return testEventStream(events...), nil
				}}
				path := "/v1beta/models/test:generateContent"
				if stream {
					path = "/v1beta/models/test:streamGenerateContent?alt=sse"
				}
				response, raw := protocolTestRequest(t, newProtocolTestServer(t, service), http.MethodPost, path,
					`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"seed":7,"candidateCount":2}}`, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status=%d: %s", response.StatusCode, raw)
				}
				var final map[string]any
				if stream {
					if response.Header.Get("Content-Type") != "text/event-stream" {
						t.Fatalf("not SSE: %v", response.Header)
					}
					frames := sseTestPayloads(t, raw)
					if len(frames) != 2 {
						t.Fatalf("want one text frame and one final frame: %s", raw)
					}
					first := frames[0]["candidates"].([]any)[0].(map[string]any)
					if first["content"] == nil || first["groundingMetadata"] != nil || first["citationMetadata"] != nil || first["finishReason"] != nil {
						t.Fatalf("metadata emitted before the final frame: %+v", first)
					}
					final = frames[len(frames)-1]
				} else if err := json.Unmarshal(raw, &final); err != nil {
					t.Fatal(err)
				}
				candidates := final["candidates"].([]any)
				if len(candidates) != 2 || final["modelVersion"] != "upstream-test" || final["responseId"] == "" {
					t.Fatalf("final response=%+v", final)
				}
				usage := final["usageMetadata"].(map[string]any)
				if usage["promptTokenCount"] != float64(20) || usage["thoughtsTokenCount"] != float64(6) || usage["totalTokenCount"] != float64(30) {
					t.Errorf("candidate usage not summed: %+v", usage)
				}
				for index, value := range candidates {
					candidate := value.(map[string]any)
					if candidate["index"] != float64(index) || candidate["finishReason"] != "STOP" || candidate["finishMessage"] != "finished" {
						t.Errorf("candidate end=%+v", candidate)
					}
					if stream {
						want := map[string]any{"role": "model", "parts": []any{}}
						if !reflect.DeepEqual(candidate["content"], want) {
							t.Errorf("empty terminal content=%+v", candidate["content"])
						}
					}
					if (candidate["groundingMetadata"] != nil) != (sources != "citations") || (candidate["citationMetadata"] != nil) != (sources != "grounding") {
						t.Fatalf("missing or invented metadata: %+v", candidate)
					}
					if sources != "grounding" {
						citations := candidate["citationMetadata"].(map[string]any)["citationSources"].([]any)
						if len(citations) != 2 || citations[0].(map[string]any)["uri"] != "https://citation.test/1" || citations[1].(map[string]any)["uri"] != "https://citation.test/2" {
							t.Errorf("native citations were replaced by grounding or lost: %+v", citations)
						}
					}
				}
			})
		}
	}
}

func TestGeminiPromptFeedbackHTTP(t *testing.T) {
	for _, stage := range []string{"generate", "first_event", "after_usage"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", stage, stream), func(t *testing.T) {
				failure := fmt.Errorf("wrapped: %w", &aistudio.PromptFeedbackError{Reason: " safety ", Message: "input blocked"})
				service := generationTestService{generate: func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					if stage == "generate" {
						return nil, failure
					}
					var events []aistudio.Event
					if stage == "after_usage" {
						events = append(events, aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 2}})
					}
					return testEventStream(append(events, aistudio.Event{Kind: aistudio.EventError, Err: failure})...), nil
				}}
				path := "/v1beta/models/test:generateContent"
				if stream {
					path = "/v1beta/models/test:streamGenerateContent?alt=sse"
				}
				response, raw := protocolTestRequest(t, newProtocolTestServer(t, service), http.MethodPost, path, `{"contents":[{"parts":[{"text":"hi"}]}]}`, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status=%d: %s", response.StatusCode, raw)
				}
				var payload map[string]any
				if stream {
					frames := sseTestPayloads(t, raw)
					if len(frames) != 1 {
						t.Fatalf("blocked input must produce only one frame: %s", raw)
					}
					payload = frames[0]
				} else if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) != 3 || payload["modelVersion"] != "test" || payload["responseId"] == "" || !reflect.DeepEqual(payload["promptFeedback"], map[string]any{"blockReason": "SAFETY", "blockReasonMessage": "input blocked"}) {
					t.Fatalf("invalid prompt feedback response: %+v", payload)
				}
			})
		}
	}
	for _, reason := range []string{"", " unspecified "} {
		payload, ok := geminiPromptFeedback(aistudio.GenerateRequest{}, &aistudio.PromptFeedbackError{Reason: reason})
		if !ok || !reflect.DeepEqual(payload["promptFeedback"], map[string]any{"blockReason": "BLOCK_REASON_UNSPECIFIED"}) {
			t.Errorf("unspecified feedback=%+v", payload)
		}
	}
}

func TestGeminiDynamicThinkingBudget(t *testing.T) {
	for _, budget := range []string{"null", "-1", "0", "1024"} {
		var request geminiRequest
		if err := json.Unmarshal([]byte(`{"generationConfig":{"thinkingConfig":{"thinkingBudget":`+budget+`,"thinkingLevel":"high","includeThoughts":true}}}`), &request); err != nil {
			t.Fatal(err)
		}
		generated, err := request.toGenerateRequest("test", "test")
		if err != nil {
			t.Fatal(err)
		}
		config := generated.Config
		if budget == "-1" || budget == "null" {
			if config.ThinkingBudget != nil {
				t.Errorf("dynamic budget should use model defaults: %+v", config)
			}
		} else if config.ThinkingBudget == nil || fmt.Sprint(*config.ThinkingBudget) != budget {
			t.Errorf("explicit budget changed: %+v", config)
		}
		if config.HideThinking || !strings.EqualFold(config.ReasoningEffort, "high") {
			t.Errorf("other thinking settings changed: %+v", config)
		}
	}
}
