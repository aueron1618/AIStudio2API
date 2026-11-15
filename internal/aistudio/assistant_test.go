package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

func assistantCatalogForTest(t *testing.T) (modelCatalog, *Account, *AccountPool) {
	t.Helper()
	row := make([]any, 65)
	row[0], row[3], row[5], row[6] = "models/shared", "Shared", 1024, 256
	row[7], row[64] = []string{"generateContent", "countTokens"}, []int64{48}
	raw, err := json.Marshal([]any{[]any{row}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := parseModelCatalog(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: catalog.models}
	return catalog, account, NewAccountPool([]*Account{account}, 1)
}

func TestAssistantChannelUsesExplicitBuildURL(t *testing.T) {
	catalog, account, pool := assistantCatalogForTest(t)
	entry, ok := catalog.lookup("shared-build")
	if !ok || !entry.model.Capabilities["assistant_route"] || entry.model.Capabilities["build_route"] {
		t.Fatalf("assistant catalog entry = %+v, %v", entry, ok)
	}
	client := NewClient(ClientOptions{})
	client.setModelCatalog(account.ID, catalog)
	service := &PooledService{pool: pool, client: client}
	// The new assistant channel remains available even when the legacy proxy is disabled.
	for _, channel := range []Channel{"", ChannelPlayground, ChannelBuild} {
		ctx := ContextWithChannel(t.Context(), channel)
		models, err := service.Models(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"generateContent", "countTokens"} {
			selection := AccountSelection{ModelID: "models/shared-build", Method: method}
			lease, err := pool.AcquireFor(ctx, selection)
			_, listed := findCatalogModel(models, "shared-build")
			if channel == ChannelPlayground {
				if lease != nil || !errors.Is(err, ErrNoEligibleAccount) || listed || len(models) != 1 || models[0].ID != "shared" {
					t.Fatalf("normal URL admitted assistant: models=%+v lease=%v err=%v listed=%v", models, lease, err, listed)
				}
				continue
			}
			if err != nil || !listed || lease == nil || lease.Channel() != ChannelAssistant {
				t.Fatalf("channel=%s method=%s lease=%v err=%v listed=%v", channel, method, lease, err, listed)
			}
			if err := validateLeaseSelection(lease, selectionWithChannel(ctx, selection)); err != nil {
				t.Fatal(err)
			}
			if lease.CooldownScope("shared-build") != "shared-build" {
				t.Fatal("assistant reused ordinary model quota scope")
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
		}
		if channel == ChannelBuild && (len(models) != 1 || models[0].ID != "shared-build" || !reflect.DeepEqual(models[0].Channels, []string{"build"})) {
			t.Fatalf("Build URL catalog = %+v", models)
		}
	}
	if lease, err := pool.AcquireFor(ContextWithChannel(t.Context(), ChannelBuild), AccountSelection{ModelID: "shared", Method: "generateContent"}); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("ordinary model silently became assistant: lease=%v err=%v", lease, err)
	}
	untagged := Model{ID: "unmarked-build", Channels: []string{"assistant"}}
	if got := FilterModelsByChannel([]Model{untagged}, ChannelBuild); len(got) != 0 {
		t.Fatalf("assistant suffix/channel without catalog capability admitted: %+v", got)
	}
}

func TestAssistantChannelRetainsAccountCapacity(t *testing.T) {
	_, account, pool := assistantCatalogForTest(t)
	ordinary, err := pool.AcquireFor(ContextWithChannel(t.Context(), ChannelPlayground), AccountSelection{ModelID: "shared", Method: "generateContent"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithChannel(t.Context(), ChannelBuild)
	selection := AccountSelection{ModelID: "shared-build", Method: "generateContent"}
	if lease, waitable, err := pool.TryAcquireFor(ctx, selection); lease != nil || !waitable || err != nil {
		t.Fatalf("assistant bypassed busy ordinary lease: lease=%v waitable=%v err=%v", lease, waitable, err)
	}
	if err := ordinary.Release(); err != nil {
		t.Fatal(err)
	}
	lease, err := pool.AcquireFor(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	releaseCleanup := lease.Retain()
	lease.finishAssistantGeneration()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if next, waitable, err := pool.TryAcquireFor(ctx, selection); next != nil || !waitable || err != nil {
		t.Fatalf("cleanup lost its capacity reservation: lease=%v waitable=%v err=%v", next, waitable, err)
	}
	if err := releaseCleanup(); err != nil {
		t.Fatal(err)
	}
	pool.SetPerAccountConcurrency(2)
	lease, err = pool.AcquireFor(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	if next, waitable, err := pool.TryAcquireFor(ctx, selection); next != nil || !waitable || err != nil {
		t.Fatalf("assistant trajectory serialization lost: lease=%v waitable=%v err=%v", next, waitable, err)
	}
	if err := lease.Release(); err != nil || account.active != 0 || account.assistantBusy {
		t.Fatalf("assistant lease leaked: active=%d busy=%v err=%v", account.active, account.assistantBusy, err)
	}
}

func TestAssistantGeneratePreservesAntiTruncationAndChannel(t *testing.T) {
	for _, anti := range []bool{false, true} {
		t.Run(fmt.Sprint(anti), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, account, pool := assistantCatalogForTest(t)
				lifecycle := NewAssistantLifecycle(t.Context())
				defer lifecycle.Stop()
				requestCtx, cancelRequest := context.WithCancel(ContextWithAssistantLifecycle(ContextWithChannel(t.Context(), ChannelBuild), lifecycle))
				defer cancelRequest()
				cleanupStarted := make(chan context.Context, 1)
				account.runtime.Assistant = &assistantApplet{ID: "applet"}
				text := "complete answer"
				protectedCalls := 0
				client := NewClient(ClientOptions{Transport: antiTruncationCountTransport(func(ctx context.Context, rpc RPCRequest) (*RPCResponse, error) {
					if ChannelFromContext(ctx) != ChannelBuild {
						t.Error("assistant RPC lost explicit Build URL context")
					}
					if rpc.Method == "SaveApplet" {
						cleanupStarted <- ctx
						<-ctx.Done()
						return nil, ctx.Err()
					}
					body := []byte(`[]`)
					if rpc.Method == assistantStreamMethod {
						step := make([]any, 16)
						step[0] = []any{[]any{[]any{[]any{nil, text}}, "model"}}
						step[11], step[12], step[15] = 1, 1, []int{4, 2, 1}
						var err error
						body, err = json.Marshal([]any{[]any{[]any{nil, nil, []any{step}, "trajectory"}}})
						if err != nil {
							return nil, err
						}
					}
					return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
				}), Protected: ProtectedTransportFunc(func(ctx context.Context, request GenerateRequest, rpc RPCRequest) (*RPCResponse, error) {
					protectedCalls++
					lease, ok := AccountLeaseFromContext(ctx)
					if !ok || lease.Channel() != ChannelAssistant || ChannelFromContext(ctx) != ChannelBuild || rpc.Method != assistantOfflineMethod {
						return nil, fmt.Errorf("wrong assistant dispatch: lease=%v method=%s", lease, rpc.Method)
					}
					if anti {
						if !request.AntiTruncation || len(request.Tools.Functions) != 1 || !strings.HasPrefix(request.Tools.Functions[0].Name, "agw_emit_") {
							return nil, errors.New("assistant lost anti-truncation tool contract")
						}
						text = "```function_calls\n[{\"name\":\"" + request.Tools.Functions[0].Name + "\",\"arguments\":{\"content\":\"complete answer\"}}]\n```"
					}
					return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(strings.NewReader(`["task","trajectory"]`))}, nil
				})})
				client.setModelCatalog(account.ID, catalog)
				client.assistants.put(account.ID, assistantOpenedApplet{applet: "applet", metadata: []any{"Title", "Description"}})
				service := &PooledService{pool: pool, client: client}
				events, err := service.Generate(requestCtx, GenerateRequest{
					Model: "shared-build", AntiTruncation: anti, Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "reply"}}}},
				})
				if err != nil {
					t.Fatal(err)
				}
				var answer strings.Builder
				finished, used := false, false
				for event := range events {
					switch event.Kind {
					case EventText:
						answer.WriteString(event.Text)
					case EventFinish:
						finished = true
					case EventUsage:
						used = event.Usage.InputTokens == 5 && event.Usage.OutputTokens == 2 && event.Usage.TotalTokens == 7
					case EventToolCall, EventError:
						t.Errorf("assistant exposed synthetic call or failed: %+v", event)
					}
				}
				cleanupCtx := <-cleanupStarted
				cancelRequest()
				synctest.Wait()
				if cleanupCtx.Err() != nil {
					t.Fatal("HTTP disconnect canceled independent assistant cleanup")
				}
				lifecycle.Stop()
				if answer.String() != "complete answer" || !finished || !used || protectedCalls != 1 || account.active != 0 || account.assistantBusy {
					t.Fatalf("answer=%q finish=%v usage=%v calls=%d active=%d busy=%v", answer.String(), finished, used, protectedCalls, account.active, account.assistantBusy)
				}
			})
		})
	}
}

func TestAssistantLifecycleRejectsLateWorkAndJoinsCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifecycle := NewAssistantLifecycle(t.Context())
		request := ContextWithAssistantLifecycle(t.Context(), lifecycle)
		request = ContextWithSelectionScope(context.Background(), request)
		captured, _ := request.Value(assistantLifecycleKey{}).(*AssistantLifecycle)
		if captured != lifecycle {
			t.Fatal("detached request lost its admitted service generation")
		}
		ctx, release, err := captured.begin(context.WithoutCancel(request))
		if err != nil {
			t.Fatal(err)
		}
		stopped := make(chan struct{})
		go func() { lifecycle.Stop(); close(stopped) }()
		<-ctx.Done()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop did not wait for retained cleanup ownership")
		default:
		}
		if _, _, err := captured.begin(context.Background()); !errors.Is(err, context.Canceled) {
			t.Fatalf("stopped generation admitted late work: %v", err)
		}
		release()
		release()
		<-stopped
		fresh := NewAssistantLifecycle(t.Context())
		_, release, err = fresh.begin(context.Background())
		if err != nil {
			t.Fatal("old cleanup stopped a new generation")
		}
		release()
		fresh.Stop()
	})
}
