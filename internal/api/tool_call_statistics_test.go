package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGenerationResultAntiTruncationToolCalls(t *testing.T) {
	failure := errors.New("invalid transport content")
	usage := &aistudio.Usage{InputTokens: 7, OutputTokens: 3, ToolTokens: 2, TotalTokens: 12}
	for _, test := range []struct {
		name     string
		events   []aistudio.Event
		internal int
		genuine  int
		finished bool
		err      error
	}{
		{name: "snapshots are not additive", events: []aistudio.Event{
			{Kind: aistudio.EventText, Text: "one", AntiTruncationToolCalls: 1},
			{Kind: aistudio.EventReasoning, Text: "thinking", AntiTruncationToolCalls: 1},
			{Kind: aistudio.EventText, Text: "two", AntiTruncationToolCalls: 2},
			{Kind: aistudio.EventToolCall, Part: aistudio.Part{FunctionCall: &aistudio.FunctionCall{ID: "real", Name: "lookup", Arguments: json.RawMessage(`{}`)}}, AntiTruncationToolCalls: 2},
			{Kind: aistudio.EventUsage, Usage: usage, AntiTruncationToolCalls: 1},
			{Kind: aistudio.EventFinish, FinishReason: "stop", AntiTruncationToolCalls: 2},
		}, internal: 2, genuine: 1, finished: true},
		{name: "no synthetic fallback", events: []aistudio.Event{
			{Kind: aistudio.EventText, Text: "ordinary"},
			{Kind: aistudio.EventFinish, FinishReason: "stop"},
		}, finished: true},
		{name: "only finish", events: []aistudio.Event{
			{Kind: aistudio.EventFinish, FinishReason: "stop_sequence", AntiTruncationToolCalls: 1},
		}, internal: 1, finished: true},
		{name: "first event error", events: []aistudio.Event{
			{Kind: aistudio.EventError, Err: failure, AntiTruncationToolCalls: 1},
		}, internal: 1, err: failure},
		{name: "later error increments snapshot", events: []aistudio.Event{
			{Kind: aistudio.EventText, Text: "one", AntiTruncationToolCalls: 1},
			{Kind: aistudio.EventError, Err: failure, AntiTruncationToolCalls: 2},
		}, internal: 2, err: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := consumeEvents(t.Context(), testEventStream(test.events...))
			if !errors.Is(err, test.err) || result.antiTruncationToolCalls != test.internal || len(result.toolCalls) != test.genuine || result.finished != test.finished {
				t.Fatalf("internal=%d genuine=%d finished=%t error=%v", result.antiTruncationToolCalls, len(result.toolCalls), result.finished, err)
			}
			for _, call := range result.toolCalls {
				if call.Name != "lookup" || call.ID != "real" {
					t.Fatalf("synthetic counter polluted genuine calls: %+v", call)
				}
			}
			if test.genuine > 0 && !reflect.DeepEqual(result.usage, usage) {
				t.Fatalf("tool statistics changed token usage: %+v", result.usage)
			}
			data, err := json.Marshal(result.events)
			if err != nil || strings.Contains(strings.ToLower(string(data)), "truncation") {
				t.Fatalf("internal metadata serialized: %s, %v", data, err)
			}
		})
	}
}

func TestAwaitStreamStartPreservesToolCounter(t *testing.T) {
	failure := errors.New("invalid transport content")
	event := aistudio.Event{Kind: aistudio.EventError, Err: failure, AntiTruncationToolCalls: 2}
	replay, err := awaitStreamStart(t.Context(), testEventStream(event))
	if !errors.Is(err, failure) || replay == nil {
		t.Fatalf("first error=%v replay=%v", err, replay)
	}
	if got := <-replay; got.Kind != event.Kind || got.Err != failure || got.AntiTruncationToolCalls != 2 {
		t.Fatalf("first error lost metadata: %+v", got)
	}
	if _, ok := <-replay; ok {
		t.Fatal("first error was replayed more than once")
	}
}

func TestChoiceToolCallStatisticsHTTP(t *testing.T) {
	for _, protocol := range []string{"chat", "gemini"} {
		for _, stream := range []bool{false, true} {
			for _, scenario := range []string{"mixed", "fallback", "first errors"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, stream, scenario), func(t *testing.T) {
					admin := &modelLogAdmin{}
					var generated atomic.Int32
					failure := errors.New("invalid transport content")
					service := generationTestService{generate: func(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
						generated.Add(1)
						if request.Model != "model" || !request.AntiTruncation {
							return nil, errors.New("transport request lost its routing flag")
						}
						if scenario == "first errors" {
							SetAccessLogGenerationResult(ctx, nil, 1)
							return testEventStream(aistudio.Event{Kind: aistudio.EventError, Err: failure, AntiTruncationToolCalls: 1}), nil
						}
						internal, genuine := 0, 0
						if scenario == "mixed" {
							internal = 1
							if strings.HasSuffix(request.ID, "-1") {
								internal, genuine = 2, 1
							} else if strings.HasSuffix(request.ID, "-2") {
								internal = 0
							}
						}
						usage := &aistudio.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
						events := []aistudio.Event{{Kind: aistudio.EventText, Text: "answer", AntiTruncationToolCalls: min(1, internal)}}
						if internal == 2 {
							events = append(events, aistudio.Event{Kind: aistudio.EventText, Text: "two", AntiTruncationToolCalls: 2})
						}
						if genuine != 0 {
							events = append(events, aistudio.Event{Kind: aistudio.EventToolCall, Part: aistudio.Part{FunctionCall: &aistudio.FunctionCall{ID: "real", Name: "lookup", Arguments: json.RawMessage(`{}`)}}, AntiTruncationToolCalls: internal})
						}
						events = append(events,
							aistudio.Event{Kind: aistudio.EventUsage, Usage: usage, AntiTruncationToolCalls: internal},
							aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop", AntiTruncationToolCalls: internal})
						SetAccessLogGenerationResult(ctx, usage, internal+genuine)
						return testEventStream(events...), nil
					}}
					handler := NewHandler(service, Config{Admin: admin, AntiTruncation: func() bool { return true }})
					model := antiTruncationModelPrefix + "model"
					path := "/v1/chat/completions"
					body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"n":3,"stream":%t}`, model, stream)
					if protocol == "gemini" {
						path = "/v1beta/models/" + model + ":generateContent"
						if stream {
							path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
						}
						body = `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":3}}`
					}
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					wantCalls := map[string]int{"mixed": 4, "fallback": 0, "first errors": 3}[scenario]
					entry := admin.entry
					if generated.Load() != 3 || entry.ToolCalls != wantCalls || entry.RequestedModel != model {
						t.Fatalf("generations=%d tool calls=%d requested=%q response=%s", generated.Load(), entry.ToolCalls, entry.RequestedModel, response.Body)
					}
					if scenario == "first errors" {
						// 客户端错误正文会脱敏，日志仍应保留上游失败原因且不伪造成功状态。
						if entry.Status != http.StatusBadGateway || !strings.Contains(entry.Error, failure.Error()) || entry.FinishReason != "" {
							t.Fatalf("failed call was reported as successful: %+v", entry)
						}
					} else if response.Code != http.StatusOK || entry.Error != "" || !reflect.DeepEqual(entry.Usage, &aistudio.Usage{InputTokens: 6, OutputTokens: 9, TotalTokens: 15}) {
						t.Fatalf("status=%d log=%+v response=%s", response.Code, entry, response.Body)
					}
					for _, hidden := range []string{"AntiTruncationToolCalls", "anti_truncation_tool_calls", "agw_emit_"} {
						if strings.Contains(response.Body.String(), hidden) {
							t.Fatalf("response exposed internal metadata %q: %s", hidden, response.Body)
						}
					}
					if scenario == "mixed" && strings.Count(response.Body.String(), `"name":"lookup"`) != 1 {
						t.Fatalf("genuine tool output changed: %s", response.Body)
					}
				})
			}
		}
	}
}
