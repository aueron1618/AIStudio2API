package aistudio

import (
	"context"
	"reflect"
	"testing"
)

func TestScopedSelectionSnapshot(t *testing.T) {
	pool := NewAccountPool(nil, 1)
	keys := map[string][]string{"key": {"a", "b"}}
	models := map[string][]string{"m": {"b", "c"}}
	pool.SetAPIKeyAccounts(keys)
	pool.SetModelAccounts(models)
	snapshot := pool.ScopedSelectionSnapshot()
	ctx := ContextWithChannel(ContextWithAccountScope(ContextWithAPIKey(context.Background(), "key"), []string{"a", "b", "c"}), ChannelBuild)
	selection := AccountSelection{ModelID: "models/m", Method: "generateContent"}
	checkSnapshot := func() {
		t.Helper()
		got := snapshot(ctx, selection)
		if got.Channel != ChannelBuild || !reflect.DeepEqual(got.AllowedAccountIDs, []string{"b"}) {
			t.Fatalf("snapshot scope changed: %+v", got)
		}
		got.AllowedAccountIDs[0] = "changed-result"
	}
	checkSnapshot()
	keys["key"][1] = "changed-input"
	models["m"][0] = "changed-input"
	checkSnapshot()
	if got := pool.ScopedSelection(ctx, selection); !reflect.DeepEqual(got.AllowedAccountIDs, []string{"b"}) {
		t.Fatalf("caller mutation changed pool bindings: %+v", got)
	}
	pool.SetAPIKeyAccounts(map[string][]string{"key": {"c"}})
	pool.SetModelAccounts(map[string][]string{"m": {"c"}})
	checkSnapshot()
	if got := pool.ScopedSelection(ctx, selection); !reflect.DeepEqual(got.AllowedAccountIDs, []string{"c"}) {
		t.Fatalf("live selection ignored updated bindings: %+v", got)
	}
	pinned := snapshot(ContextWithAccount(ctx, "a"), selection)
	if pinned.AccountID != "a" || !reflect.DeepEqual(pinned.AllowedAccountIDs, []string{"a", "b"}) {
		t.Fatalf("snapshot lost pinned-account scope: %+v", pinned)
	}
	selection.AllowedAccountIDs = []string{}
	if got := snapshot(ctx, selection); got.AllowedAccountIDs == nil || len(got.AllowedAccountIDs) != 0 {
		t.Fatalf("explicit empty scope broadened: %+v", got)
	}
	if got := snapshot(context.Background(), AccountSelection{}); got.AllowedAccountIDs != nil {
		t.Fatalf("unbound scope was restricted: %+v", got)
	}
}

// TestChannelSelection 保留文件绑定与专属能力的通道资格
func TestChannelSelection(t *testing.T) {
	pool := NewAccountPool(nil, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	for _, item := range []struct {
		selection AccountSelection
		want      []Channel
	}{
		{AccountSelection{ModelID: "gemini", Method: "generateContent"}, []Channel{ChannelPlayground, ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", Channel: ChannelBuild}, []Channel{ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", ResourceID: "file", Channel: ChannelBuild}, nil},
		{AccountSelection{ModelID: "gemini", Method: "countTokens"}, []Channel{ChannelPlayground, ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "countTokens", Channel: ChannelBuild}, []Channel{ChannelBuild}},
		{AccountSelection{ModelID: "gemini", Method: "generateContent", PlaygroundOnly: true, Channel: ChannelBuild}, nil},
	} {
		if got := pool.selectionChannelsLocked(item.selection); !reflect.DeepEqual(got, item.want) {
			t.Errorf("selection=%+v channels=%v want=%v", item.selection, got, item.want)
		}
	}
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	if got := pool.selectionChannelsLocked(AccountSelection{ModelID: "gemini", Method: "generateContent", Channel: ChannelBuild}); len(got) != 0 {
		t.Fatalf("disabled Build channels=%v", got)
	}
}
