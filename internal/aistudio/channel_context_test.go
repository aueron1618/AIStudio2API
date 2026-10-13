package aistudio

import (
	"context"
	"testing"
	"time"
)

func TestContextChannelLease(t *testing.T) {
	model := Model{ID: "shared", Methods: []string{"generateContent"}}
	account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
		t.Fatal(err)
	}
	selection := AccountSelection{ModelID: model.ID, Method: "generateContent", PreferredChannel: ChannelBuild}
	for _, channel := range []Channel{ChannelPlayground, ChannelBuild} {
		ctx := ContextWithChannel(context.Background(), channel)
		lease, _, err := pool.TryAcquireFor(ctx, selection)
		if err != nil || lease == nil {
			t.Fatalf("channel=%s lease=%v err=%v", channel, lease, err)
		}
		if lease.Channel() != channel {
			t.Fatalf("got=%s want=%s", lease.Channel(), channel)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.MarkCooldownIfGeneration(account.ID, ChannelCooldownScope(ChannelBuild, model.ID), 1, time.Now(), time.Now().Add(time.Hour), "test"); err != nil {
		t.Fatal(err)
	}
	lease, waitable, err := pool.TryAcquireFor(ContextWithChannel(context.Background(), ChannelBuild), selection)
	if lease != nil || !waitable || err != nil {
		t.Fatalf("must not fall back: lease=%v err=%v", lease, err)
	}
}
