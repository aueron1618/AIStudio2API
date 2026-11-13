package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

type toolStatisticsAdmin struct {
	api.AdminService
	entry api.AccessLog
}

func (*toolStatisticsAdmin) RecordAccessStart(api.AccessLog) {}
func (admin *toolStatisticsAdmin) RecordAccessLog(entry api.AccessLog) {
	admin.entry = entry
}

func TestTrackedAntiTruncationToolStatistics(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scenario := range []string{"snapshots", "mixed", "fallback", "first error", "later error", "retry winner", "exhausted retries"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, scenario), func(t *testing.T) {
				failure := errors.New("invalid transport content")
				interrupted := &aistudio.StreamInterruptedError{Err: io.ErrUnexpectedEOF}
				generated := 0
				service := retryTestRuntime(t, func(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					generated++
					if request.Model != "shared" || !request.AntiTruncation {
						return nil, errors.New("incorrect anti-truncation routing")
					}
					calls := 1
					if scenario == "first error" || scenario == "retry winner" && generated == 1 {
						err := failure
						if scenario == "retry winner" {
							calls, err = 9, interrupted
						}
						result := make(chan aistudio.Event, 1)
						result <- aistudio.Event{Kind: aistudio.EventError, Err: err, AntiTruncationToolCalls: calls}
						close(result)
						return result, nil
					}
					if scenario == "fallback" {
						calls = 0
					}
					if scenario == "mixed" || scenario == "exhausted retries" {
						calls = 2
					}
					items := []aistudio.Event{
						{Kind: aistudio.EventText, Text: "answer", AntiTruncationToolCalls: min(1, calls)},
						{Kind: aistudio.EventReasoning, Text: "thinking", AntiTruncationToolCalls: min(1, calls)},
					}
					if scenario == "mixed" {
						items = append(items, aistudio.Event{Kind: aistudio.EventToolCall,
							Part: aistudio.Part{FunctionCall: &aistudio.FunctionCall{ID: "real", Name: "lookup", Arguments: json.RawMessage(`{}`)}}, AntiTruncationToolCalls: calls})
					}
					if scenario == "later error" || scenario == "exhausted retries" {
						err := failure
						if scenario == "exhausted retries" {
							err = interrupted
						}
						items = append(items, aistudio.Event{Kind: aistudio.EventError, Err: err, AntiTruncationToolCalls: calls})
					} else {
						items = append(items,
							aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}, AntiTruncationToolCalls: calls},
							aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "STOP", AntiTruncationToolCalls: calls})
					}
					result := make(chan aistudio.Event, len(items))
					for _, event := range items {
						result <- event
					}
					close(result)
					return result, nil
				})
				admin := &toolStatisticsAdmin{}
				handler := api.NewHandler(retryTestService{generate: func(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
					ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					service.requests.start(request, cancel)
					events := make(chan aistudio.Event)
					acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool, _ *int) (*aistudio.AccountLease, error) {
						return service.pool.AcquireFor(ctx, selection)
					}
					go service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
					return events, nil
				}}, api.Config{Admin: admin, AntiTruncation: func() bool { return true }})
				body := fmt.Sprintf(`{"model":"\u6297\u622a\u65ad-shared","messages":[{"role":"user","content":"hi"}],"stream":%t}`, stream)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
				wantCalls := 1
				if scenario == "mixed" {
					wantCalls = 3
				} else if scenario == "fallback" {
					wantCalls = 0
				} else if scenario == "exhausted retries" {
					wantCalls = 2
				}
				failed := scenario == "first error" || scenario == "later error" || scenario == "exhausted retries"
				entry := admin.entry
				if entry.ToolCalls != wantCalls || (entry.Error != "") != failed {
					t.Fatalf("observed calls=%d want=%d error=%q: %s", entry.ToolCalls, wantCalls, entry.Error, w.Body)
				}
				if !failed && (w.Code != http.StatusOK || entry.ResponseModel != "shared") {
					t.Fatalf("response status=%d model=%q: %s", w.Code, entry.ResponseModel, w.Body)
				}
				if scenario == "retry winner" && generated != 2 {
					t.Fatalf("expected a failed attempt and a winner, got %d", generated)
				}
				for _, hidden := range []string{"AntiTruncationToolCalls", "anti_truncation_tool_calls", "agw_emit_"} {
					if strings.Contains(w.Body.String(), hidden) {
						t.Fatalf("internal tool statistics leaked: %s", w.Body)
					}
				}
				assertRetryLeasesReleased(t, service)
			})
		}
	}
}

func TestStreamToolStatisticsPublishedBeforeSourceClose(t *testing.T) {
	source := make(chan aistudio.Event, 2)
	source <- aistudio.Event{Kind: aistudio.EventText, Text: "answer", AntiTruncationToolCalls: 1}
	source <- aistudio.Event{Kind: aistudio.EventError, Err: errors.New("fixture terminal error"), AntiTruncationToolCalls: 1}
	service := retryTestRuntime(t, func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
		return source, nil
	})
	done := make(chan struct{})
	defer func() {
		close(source)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("tracked stream did not release its lease")
		}
	}()
	admin := &toolStatisticsAdmin{}
	handler := api.NewHandler(retryTestService{generate: func(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
		// Keep the upstream open so the handler must observe statistics before draining.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		service.requests.start(request, cancel)
		events := make(chan aistudio.Event)
		acquire := func(ctx context.Context, selection aistudio.AccountSelection, _ bool, _ *int) (*aistudio.AccountLease, error) {
			return service.pool.AcquireFor(ctx, selection)
		}
		go func() {
			service.generateWithRetry(ctx, ctx, cancel, time.Now(), request, "", acquire, events)
			close(done)
		}()
		return events, nil
	}}, api.Config{Admin: admin, AntiTruncation: func() bool { return true }})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"\u6297\u622a\u65ad-shared","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	observed := admin.entry
	if observed.ToolCalls != 1 || observed.Error == "" {
		t.Fatalf("terminal response raced ahead of statistics: calls=%d error=%q", observed.ToolCalls, observed.Error)
	}
}

func TestGenerateEventCollectorsPreserveToolCalls(t *testing.T) {
	failure := errors.New("invalid transport content")
	source := make(chan aistudio.Event, 2)
	source <- aistudio.Event{Kind: aistudio.EventError, Err: failure, AntiTruncationToolCalls: 1}
	source <- aistudio.Event{Kind: aistudio.EventUsage, AntiTruncationToolCalls: 2}
	close(source)
	first, err := firstGenerateEvent(t.Context(), source, nil)
	if !errors.Is(err, failure) || first.Kind != aistudio.EventError || first.AntiTruncationToolCalls != 2 {
		t.Fatalf("first error lost observed calls: %+v, %v", first, err)
	}
	interrupted := &aistudio.StreamInterruptedError{Err: io.ErrUnexpectedEOF}
	source = make(chan aistudio.Event, 2)
	source <- aistudio.Event{Kind: aistudio.EventText, Text: "partial", AntiTruncationToolCalls: 1}
	source <- aistudio.Event{Kind: aistudio.EventError, Err: interrupted, AntiTruncationToolCalls: 3}
	close(source)
	replay, count, err := collectGenerateEvents(t.Context(), source, nil)
	if !errors.Is(err, interrupted) || replay != nil || count != 3 {
		t.Fatalf("discarded partial generation lost calls: count=%d replay=%v error=%v", count, replay, err)
	}
}
