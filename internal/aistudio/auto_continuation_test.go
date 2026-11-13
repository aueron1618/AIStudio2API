package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

type continuationEOFReader struct {
	io.Reader
	atEOF func()
}

func (reader *continuationEOFReader) Read(data []byte) (int, error) {
	n, err := reader.Reader.Read(data)
	if err == io.EOF && reader.atEOF != nil {
		reader.atEOF()
		reader.atEOF = nil
	}
	return n, err
}

func TestAutoContinuationPolicy(t *testing.T) {
	for _, test := range []struct {
		name       string
		disabled   bool
		finish     int
		token      string
		change     string
		wantCalls  int
		wantFinish string
		wantError  bool
	}{
		{name: "default-enabled-explicit", finish: 22, token: "next", wantCalls: 2, wantFinish: "stop"},
		{name: "default-enabled-max-tokens", finish: 2, token: "next", wantCalls: 2, wantFinish: "stop"},
		{name: "enabled-requires-token", finish: 22, wantCalls: 1, wantError: true},
		{name: "disabled-explicit", disabled: true, finish: 22, token: "next", wantCalls: 1, wantFinish: "continuation"},
		{name: "disabled-explicit-without-token", disabled: true, finish: 22, wantCalls: 1, wantFinish: "continuation"},
		{name: "disabled-max-tokens", disabled: true, finish: 2, token: "next", wantCalls: 1, wantFinish: "max_tokens"},
		{name: "disabled-max-tokens-without-token", disabled: true, finish: 2, wantCalls: 1, wantFinish: "max_tokens"},
		{name: "disabled-normal-stop", disabled: true, finish: 1, token: "next", wantCalls: 1, wantFinish: "stop"},
		{name: "disable-before-next-explicit", finish: 22, token: "next", change: "disable", wantCalls: 1, wantFinish: "continuation"},
		{name: "disable-before-next-max-tokens", finish: 2, token: "next", change: "disable", wantCalls: 1, wantFinish: "continuation"},
		{name: "disable-before-missing-token-check", finish: 22, change: "disable", wantCalls: 1, wantFinish: "continuation"},
		{name: "disable-in-continuation-observer", finish: 22, token: "next", change: "observer", wantCalls: 1, wantFinish: "continuation"},
		{name: "enabling-does-not-upgrade-single-round", disabled: true, finish: 22, token: "next", change: "enable", wantCalls: 1, wantFinish: "continuation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			var client *Client
			client = NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
				calls++
				if calls > 2 {
					t.Fatal("unexpected additional upstream request")
				}
				if calls == 1 && request.continuationProof != "" || calls == 2 && request.continuationProof != "!first-round" {
					t.Fatalf("continuation did not reuse its first-round proof: %q", request.continuationProof)
				}
				var wire []any
				if err := json.Unmarshal(rpc.Body, &wire); err != nil {
					t.Fatal(err)
				}
				settings := wire[3].([]any)
				if test.disabled {
					if wire[10] != float64(1) || len(settings) > 45 && settings[45] != nil || len(wire) > 16 && wire[16] != nil {
						t.Fatalf("disabled request declared continuation: %s", rpc.Body)
					}
					if settings[3] != float64(continuationRoundTokens) {
						t.Fatalf("disabled request exceeded single-round limit: %v", settings[3])
					}
				} else {
					if wire[10] != nil || len(settings) <= 45 || settings[45] != float64(1) {
						t.Fatalf("enabled request lost continuation flags: %s", rpc.Body)
					}
					if calls == 2 && (len(wire) <= 16 || wire[16] != "next" || settings[3] != float64(2*continuationRoundTokens)) {
						t.Fatalf("next round lost token or cumulative limit: %s", rpc.Body)
					}
				}
				candidate := make([]any, 9)
				candidate[1], candidate[8] = test.finish, test.token
				if calls == 2 {
					candidate[1], candidate[8] = 1, nil
				}
				frame := []any{[]any{candidate}}
				body, err := json.Marshal([]any{[]any{frame}})
				if err != nil {
					t.Fatal(err)
				}
				var reader io.Reader = bytes.NewReader(body)
				if calls == 1 && (test.change == "disable" || test.change == "enable") {
					reader = &continuationEOFReader{Reader: reader, atEOF: func() { client.SetAutoContinuation(test.change == "enable") }}
				}
				return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(reader), Proof: "!first-round"}, nil
			})})
			if test.disabled {
				NewPooledService(NewAccountPool(nil, 1), client).SetAutoContinuation(false)
			}
			limit := int64(2 * continuationRoundTokens)
			request := GenerateRequest{
				Model: "test", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}},
				Config: GenerationConfig{MaxOutputTokens: &limit},
			}
			entry := modelEntry{defaults: GenerationDefaults{MaxOutputTokens: continuationRoundTokens, ContinuedOutputTokens: limit}}
			ctx := t.Context()
			if test.change == "observer" {
				ctx = ContextWithContinuationObserver(ctx, func() { client.SetAutoContinuation(false) })
			}
			response, decode, err := client.sendPlayground(ctx, request, entry, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			finish := ""
			err = decode(response.Body, func(event Event) error {
				if event.Kind == EventFinish {
					finish = event.FinishReason
				}
				return nil
			})
			if test.wantError {
				var protocol *ProtocolEvidenceError
				if !errors.As(err, &protocol) {
					t.Fatalf("expected missing-token protocol error, got %v", err)
				}
			} else if err != nil || finish != test.wantFinish {
				t.Fatalf("finish=%q error=%v, want %q", finish, err, test.wantFinish)
			}
			if calls != test.wantCalls {
				t.Fatalf("upstream calls=%d, want %d", calls, test.wantCalls)
			}
		})
	}
}

func TestContinuationGuardRechecksAfterPreparation(t *testing.T) {
	var client *Client
	client = NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(ctx context.Context, _ GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
		client.SetAutoContinuation(false)
		return nil, checkContinuation(ctx)
	})})
	_, err := client.doProtected(t.Context(), GenerateRequest{continuationRound: 1, continuationToken: "next"}, nil)
	if !errors.Is(err, errAutoContinuationDisabled) {
		t.Fatalf("continuation guard did not survive preparation: %v", err)
	}
	ctx := context.WithValue(t.Context(), continuationPolicyContextKey{}, &client.autoContinuationDisabled)
	_, err = (&WorkerProtectedTransport{}).doPrepared(ctx, "", "", 5, "", AccountSelection{}, RPCRequest{})
	if !errors.Is(err, errAutoContinuationDisabled) {
		t.Fatalf("disabled continuation reached the worker: %v", err)
	}
}

func TestContinuationDisablePreservesPriorUsage(t *testing.T) {
	frame := func(usage []any) []byte {
		candidate := make([]any, 9)
		candidate[1], candidate[8] = 22, "next"
		body, err := json.Marshal([]any{[]any{[]any{[]any{candidate}, nil, usage}}})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	calls := 0
	var client *Client
	client = NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error) {
		calls++
		if calls > 1 {
			t.Fatal("a third round was sent after disabling continuation")
		}
		reader := &continuationEOFReader{Reader: bytes.NewReader(frame(nil)), atEOF: func() { client.SetAutoContinuation(false) }}
		return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(reader)}, nil
	})})
	request := GenerateRequest{Model: "test", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}}}
	defaults := GenerationDefaults{MaxOutputTokens: continuationRoundTokens, ContinuedOutputTokens: 3 * continuationRoundTokens}
	var usage *Usage
	finish := ""
	err := client.decodePlaygroundRounds(t.Context(), request, defaults, RequestContext{}, bytes.NewReader(frame([]any{10, 4, 16, nil, nil, nil, nil, 0, nil, 2})), func(event Event) error {
		if event.Kind == EventUsage {
			usage = event.Usage
		}
		if event.Kind == EventFinish {
			finish = event.FinishReason
		}
		return nil
	}, nil)
	if err != nil || calls != 1 || finish != "continuation" || usage == nil || usage.OutputTokens != 4 || usage.ReasoningTokens != 2 || usage.TotalTokens != 16 {
		t.Fatalf("prior usage lost: calls=%d finish=%s usage=%+v error=%v", calls, finish, usage, err)
	}
}

func TestSingleRoundEncodingDropsContinuationToken(t *testing.T) {
	request := GenerateRequest{
		Model: "test", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}},
		continuationRound: 5, continuationToken: "must-not-be-sent",
	}
	body, err := EncodeGenerateContentRequest(request, GenerationDefaults{MaxOutputTokens: continuationRoundTokens}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var wire []any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) > 16 && wire[16] != nil {
		t.Fatalf("single-round request leaked continuation token: %s", body)
	}
}

func TestContinuationAccountSwitchResetsProofAndHonorsPolicy(t *testing.T) {
	for _, disableOnSwitch := range []bool{false, true} {
		t.Run(map[bool]string{false: "continue", true: "disable-on-switch"}[disableOnSwitch], func(t *testing.T) {
			frame := func(finish int, token string) []byte {
				candidate := make([]any, 9)
				candidate[1], candidate[8] = finish, token
				body, err := json.Marshal([]any{[]any{[]any{[]any{candidate}, nil, []any{10, 4, 14}}}})
				if err != nil {
					t.Fatal(err)
				}
				return body
			}
			calls := 0
			client := NewClient(ClientOptions{Protected: ProtectedTransportFunc(func(_ context.Context, request GenerateRequest, _ RPCRequest) (*RPCResponse, error) {
				calls++
				switch calls {
				case 1:
					if request.AccountID != "a" || request.continuationProof != "!a" || request.continuationToken != "next" {
						t.Fatalf("initial continuation lost account or proof: %+v", request)
					}
					return nil, &RPCError{StatusCode: http.StatusTooManyRequests}
				case 2:
					if request.AccountID != "b" || request.continuationProof != "" || request.continuationRound != 1 || request.continuationToken != "next" {
						t.Fatalf("account switch reused another account's proof: %+v", request)
					}
				case 3:
					if request.AccountID != "b" || request.continuationProof != "!b" || request.continuationToken != "last" {
						t.Fatalf("replacement account did not retain its proof: %+v", request)
					}
				default:
					t.Fatal("unexpected extra continuation request")
				}
				body := frame(22, "last")
				if calls == 3 {
					body = frame(1, "")
				}
				return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(body)), Proof: "!b"}, nil
			})})
			pool := NewAccountPool([]*Account{{ID: "b", Config: AccountConfig{Enabled: true}, State: AccountReady}}, 1)
			lease, err := pool.AcquireFor(t.Context(), AccountSelection{AccountID: "b"})
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			switches := 0
			ctx := ContextWithContinuationAccounts(t.Context(), func(ctx context.Context, _ error) (context.Context, error) {
				if disableOnSwitch {
					client.SetAutoContinuation(false)
				}
				return ContextWithAccountLease(ctx, lease), nil
			})
			request := GenerateRequest{Model: "model", AccountID: "a", continuationProof: "!a", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}}}
			defaults := GenerationDefaults{MaxOutputTokens: continuationRoundTokens, ContinuedOutputTokens: 3 * continuationRoundTokens}
			finish := ""
			err = client.decodePlaygroundRounds(ctx, request, defaults, RequestContext{}, bytes.NewReader(frame(22, "next")), func(event Event) error {
				if event.Kind == EventFinish {
					finish = event.FinishReason
				}
				return nil
			}, func(_ context.Context, accountID string) {
				switches++
				if accountID != "b" {
					t.Fatalf("switched to unexpected account %q", accountID)
				}
			})
			wantCalls, wantFinish := 3, "stop"
			if disableOnSwitch {
				wantCalls, wantFinish = 1, "continuation"
			}
			if err != nil || calls != wantCalls || finish != wantFinish || switches != 1 {
				t.Fatalf("account switch lost continuation policy: calls=%d finish=%q switches=%d err=%v", calls, finish, switches, err)
			}
		})
	}
}
