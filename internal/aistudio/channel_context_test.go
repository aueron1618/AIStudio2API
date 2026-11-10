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

func TestContextChannelAndAPIKeyScope(t *testing.T) {
	model := Model{ID: "shared", Methods: []string{"generateContent"}}
	accounts := []*Account{
		{ID: "a", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}},
		{ID: "b", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}},
	}
	pool := NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	pool.SetAPIKeyAccounts(map[string][]string{"limited": {"b"}})
	for _, account := range accounts {
		if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
			t.Fatal(err)
		}
	}
	for _, channel := range []Channel{ChannelPlayground, ChannelBuild} {
		t.Run(string(channel), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ContextWithAPIKey(ContextWithChannel(t.Context(), channel), "limited"), time.Second)
			defer cancel()
			selection := AccountSelection{ModelID: model.ID, Method: "generateContent", PreferredChannel: ChannelBuild}
			for _, acquire := range []func(context.Context, AccountSelection) (*AccountLease, error){
				pool.AcquireFor,
				func(ctx context.Context, selection AccountSelection) (*AccountLease, error) {
					lease, _, err := pool.TryAcquireFor(ctx, selection)
					return lease, err
				},
			} {
				lease, err := acquire(ctx, selection)
				if err != nil || lease == nil || lease.Account().ID != "b" || lease.Channel() != channel {
					t.Fatalf("key or URL scope lost: lease=%v err=%v", lease, err)
				}
				if err := lease.Release(); err != nil {
					t.Fatal(err)
				}
				denied := selection
				denied.AccountID = "a"
				if lease, err := acquire(ctx, denied); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
					t.Fatalf("explicit account escaped key scope: lease=%v err=%v", lease, err)
				}
			}
			groups, err := pool.ClassifyCandidates(ctx, selection, []string{"a", "b"})
			if err != nil || !slices.Equal(groups.WarmReady, []string{"b"}) {
				t.Fatalf("worker candidates escaped key scope: groups=%+v err=%v", groups, err)
			}
			selection.AllowedAccountIDs = []string{"a"}
			if lease, err := pool.AcquireFor(ctx, selection); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
				t.Fatalf("disjoint allowed accounts escaped key scope: lease=%v err=%v", lease, err)
			}
		})
	}
}

func TestContextChannelAndModelAccountScope(t *testing.T) {
	model := Model{ID: "shared", Methods: []string{"generateContent"}}
	accounts := []*Account{
		{ID: "a", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}},
		{ID: "b", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}},
		{ID: "c", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}},
	}
	pool := NewAccountPool(accounts, 1)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	pool.SetRoutingStrategy("fill-first")
	pool.SetAccountOrder([]string{"c", "b", "a"})
	pool.SetAPIKeyAccounts(map[string][]string{"limited": {"b", "c"}})
	pool.SetModelAccounts(map[string][]string{model.ID: {"a", "b"}})
	for _, account := range accounts {
		if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
			t.Fatal(err)
		}
	}
	for _, channel := range []Channel{ChannelPlayground, ChannelBuild} {
		t.Run(string(channel), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ContextWithChannel(t.Context(), channel), time.Second)
			defer cancel()
			selection := AccountSelection{ModelID: "models/" + model.ID, Method: "generateContent", PreferredChannel: ChannelBuild}
			for _, key := range []string{"", "limited"} {
				keyCtx := ContextWithAPIKey(ctx, key)
				lease, err := pool.AcquireFor(keyCtx, selection)
				if err != nil || lease == nil || lease.Account().ID != "b" || lease.Channel() != channel {
					t.Fatalf("model binding, order or URL boundary lost: key=%q lease=%v error=%v", key, lease, err)
				}
				if err := lease.Release(); err != nil {
					t.Fatal(err)
				}
			}
			ctx = ContextWithAPIKey(ctx, "limited")
			groups, err := pool.ClassifyCandidates(ctx, selection, []string{"a", "b", "c"})
			if err != nil || !slices.Equal(groups.WarmReady, []string{"b"}) {
				t.Fatalf("worker candidates escaped model/key intersection: %+v, %v", groups, err)
			}
			selection.AllowedAccountIDs = []string{"c"}
			if lease, err := pool.AcquireFor(ctx, selection); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
				t.Fatalf("disjoint model binding allowed an automatic request: %v, %v", lease, err)
			}
			selection.AllowedAccountIDs, selection.AccountID = nil, "c"
			lease, _, err := pool.TryAcquireFor(ctx, selection)
			if err != nil || lease == nil || lease.Account().ID != "c" || lease.Channel() != channel {
				t.Fatalf("explicit key-allowed account rejected: %v, %v", lease, err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			selection.AccountID = "a"
			if lease, _, err := pool.TryAcquireFor(ctx, selection); lease != nil || !errors.Is(err, ErrNoEligibleAccount) {
				t.Fatalf("model binding bypassed API key restriction: %v, %v", lease, err)
			}
		})
	}
}
