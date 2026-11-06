package app

import (
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestRequestRowModelFields(t *testing.T) {
	for _, tc := range []struct{ name, model, requested, response string }{
		{"independent", "resolved-model", "requested-alias", "response-json-model"},
		{"requested_only", "resolved-model", "requested-alias", ""},
		{"response_only", "resolved-model", "", "response-json-model"},
		{"legacy", "resolved-model", "", ""},
		{"no_resolved_model", "", "requested-alias", "response-json-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := api.AccessLog{Model: tc.model, RequestedModel: tc.requested, ResponseModel: tc.response, Status: 200}
			row := requestRow(entry, time.Now())
			if row.Model != tc.model || row.RequestedModel != tc.requested || row.ResponseModel != tc.response {
				t.Fatalf("row=%+v", row)
			}
			if data := requestLogData(entry); data.Model != tc.model {
				t.Fatalf("request log model=%q want=%q", data.Model, tc.model)
			}
		})
	}
}

func TestRequestRowReusesAverageTokensPerSecond(t *testing.T) {
	for _, latency := range []time.Duration{0, -time.Second, time.Second + 500*time.Microsecond} {
		entry := api.AccessLog{Status: 200, Latency: latency, FirstEvent: 700 * time.Millisecond,
			Usage: &aistudio.Usage{OutputTokens: 20, ReasoningTokens: 5}}
		data := requestLogData(entry)
		row := requestRow(entry, time.Now())
		want := 0.0
		if latency > 0 {
			want = 25 / latency.Seconds()
		}
		if row.AverageTokensPerSecond != data.Usage.AverageTokensPerSecond || row.AverageTokensPerSecond != want {
			t.Fatalf("average changed between log and ledger: row=%v log=%v want=%v", row.AverageTokensPerSecond, data.Usage.AverageTokensPerSecond, want)
		}
	}
}

func TestRequestProtocolChannelPrefixes(t *testing.T) {
	for _, tc := range []struct{ path, protocol string }{
		{"/v1/chat/completions", "openai-chat"},
		{"/v1/responses", "openai-responses"},
		{"/v1/responses/resp_123/input_items", "openai-responses"},
		{"/v1/messages", "anthropic"},
		{"/v1/messages/count_tokens", "anthropic"},
		{"/v1/interactions", "interactions"},
		{"/v1/files", "files"},
		{"/v1/images/generations", "images"},
		{"/v1/audio/speech", "audio"},
		{"/v1/videos", "videos"},
		{"/v1/models", "other"},
	} {
		for _, prefix := range []string{"", "/build"} {
			t.Run(prefix+tc.path, func(t *testing.T) {
				if got := requestProtocol(prefix + tc.path); got != tc.protocol {
					t.Fatalf("protocol=%q want=%q", got, tc.protocol)
				}
			})
		}
	}
	for _, tc := range []struct{ path, protocol string }{
		{"/v1beta/models/gemini:generateContent", "gemini"},
		{"/build/v10/chat/completions", "other"},
		{"/builder/v1/chat/completions", "other"},
	} {
		if got := requestProtocol(tc.path); got != tc.protocol {
			t.Errorf("path=%s protocol=%q want=%q", tc.path, got, tc.protocol)
		}
	}
}

func TestRequestOutcomeWithObservedToolCalls(t *testing.T) {
	for _, test := range []struct {
		name, finish, state string
		calls, status       int
		err                 string
	}{
		{name: "transport stop", finish: "STOP", calls: 1, status: 200, state: "tool_calls"},
		{name: "ordinary stop", finish: "stop", status: 200, state: "completed"},
		{name: "local stop", finish: "stop_sequence", calls: 2, status: 200, state: "tool_calls"},
		{name: "limit after tool call", finish: "MAX_TOKENS", calls: 1, status: 200, state: "limited"},
		{name: "invalid transport", calls: 1, status: 502, err: "invalid transport content", state: "failed"},
		{name: "cancelled", calls: 1, status: 499, state: "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := api.AccessLog{Model: "model", RequestedModel: "\u6297\u622a\u65ad-model", ResponseModel: "model",
				FinishReason: test.finish, ToolCalls: test.calls, Status: test.status, Error: test.err}
			row := requestRow(entry, time.Now())
			if row.State != test.state || row.ToolCalls != test.calls || row.RequestedModel != entry.RequestedModel || row.ResponseModel != entry.ResponseModel {
				t.Fatalf("outcome lost calls, state or raw audit model: %+v", row)
			}
		})
	}
}
