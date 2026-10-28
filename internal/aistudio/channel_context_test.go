package aistudio

import (
	"context"
	"errors"
	"slices"
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

func TestAssistantRouteKeepsURLChannelBoundary(t *testing.T) {
	model := Model{ID: "shared-build", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"assistant_route": true}}
	account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	selection := AccountSelection{ModelID: model.ID, Method: "generateContent"}
	for _, channel := range []Channel{"", ChannelBuild, ChannelPlayground} {
		ctx, cancel := context.WithTimeout(ContextWithChannel(t.Context(), channel), time.Second)
		lease, err := pool.AcquireFor(ctx, selection)
		cancel()
		if channel == ChannelPlayground {
			if lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
				t.Fatalf("Playground crossed into assistant: lease=%v err=%v", lease, err)
			}
			continue
		}
		if err != nil || lease == nil || lease.Channel() != ChannelAssistant {
			t.Fatalf("channel=%q lease=%v err=%v", channel, lease, err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	models := pool.EligibleModels([]Model{model})
	if got := FilterModelsByChannel(models, ChannelPlayground); len(got) != 0 {
		t.Fatalf("assistant listed on Playground: %+v", got)
	}
	if got := FilterModelsByChannel(models, ChannelBuild); len(got) != 1 || !slices.Equal(got[0].Channels, []string{"build"}) {
		t.Fatalf("assistant absent from Build: %+v", got)
	}
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	if got := pool.EligibleModels([]Model{model}); len(got) != 0 {
		t.Fatalf("disabled Build still advertises assistant: %+v", got)
	}
}

func TestModelRestrictionDoesNotOverrideURLChannel(t *testing.T) {
	model := Model{ID: "shared", Methods: []string{"generateContent"}}
	account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
		t.Fatal(err)
	}
	pool.SetModelChannels(map[string]Channel{model.ID: ChannelBuild})
	selection := AccountSelection{ModelID: model.ID, Method: "generateContent"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if lease, err := pool.AcquireFor(ContextWithChannel(ctx, ChannelPlayground), selection); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("model restriction crossed URL boundary: lease=%v err=%v", lease, err)
	}
	lease, err := pool.AcquireFor(ContextWithChannel(ctx, ChannelBuild), selection)
	if err != nil || lease == nil || lease.Channel() != ChannelBuild {
		t.Fatalf("matching route rejected: lease=%v err=%v", lease, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}
