package aistudio

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestPlanLimitCooldown(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	var previous CooldownState
	for index, delay := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour} {
		next := planLimitCooldown(previous, index > 0, now, "plan limit")
		if next.Level != min(index+1, 5) || !next.Until.Equal(now.Add(delay)) || next.Reason != "plan limit" {
			t.Fatalf("attempt=%d cooldown=%+v want delay=%s", index+1, next, delay)
		}
		if active := planLimitCooldown(next, true, now.Add(30*time.Second), "concurrent limit"); active != next {
			t.Fatalf("active cooldown changed: got=%+v want=%+v", active, next)
		}
		previous, now = next, next.Until
	}
	for _, level := range []int{0, -1, 100, int(^uint(0) >> 1)} {
		previous := CooldownState{Until: now.Add(-time.Second), Level: level}
		next := planLimitCooldown(previous, true, now, "plan limit")
		wantLevel, delay := 1, time.Minute
		if level > 0 {
			wantLevel, delay = 5, time.Hour
		}
		if next.Level != wantLevel || !next.Until.Equal(now.Add(delay)) {
			t.Fatalf("previous level=%d cooldown=%+v", level, next)
		}
	}
}

func TestQuotaCooldownConcurrent(t *testing.T) {
	account := &Account{ID: "account", modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 1)
	checkedAt := time.Now().UTC()
	cooldown := QuotaCooldown{Until: checkedAt.Add(time.Minute), Reason: "first limit", Escalate: true}
	until, err := pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, checkedAt, cooldown)
	if err != nil || until.IsZero() {
		t.Fatalf("until=%v err=%v", until, err)
	}
	const attempts = 16
	var results [attempts]time.Time
	var failures [attempts]error
	var writers sync.WaitGroup
	start := make(chan struct{})
	for index := range attempts {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			results[index], failures[index] = pool.MarkQuotaCooldownIfGeneration(
				account.ID, "shared", 1, checkedAt.Add(time.Duration(index+1)*time.Millisecond),
				QuotaCooldown{Until: checkedAt.Add(time.Hour), Reason: "concurrent limit", Escalate: true},
			)
		}()
	}
	close(start)
	writers.Wait()
	for index := range attempts {
		if failures[index] != nil || !results[index].IsZero() && !results[index].Equal(until) {
			t.Fatalf("attempt=%d until=%v err=%v want=%v or skipped", index, results[index], failures[index], until)
		}
	}
	current := account.runtime.Cooldowns["shared"]
	if current.Level != 1 || !current.Until.Equal(until) || current.Reason != cooldown.Reason ||
		!account.runtime.ModelAccess["shared"].CheckedAt.Equal(checkedAt.Add(attempts*time.Millisecond)) {
		t.Fatalf("concurrent state=%+v access=%+v", current, account.runtime.ModelAccess["shared"])
	}
}

func TestQuotaCooldownStaleResults(t *testing.T) {
	for _, escalate := range []bool{false, true} {
		for _, test := range []struct {
			name       string
			generation uint64
			delay      time.Duration
		}{
			{"old generation", 1, time.Second},
			{"old checked time", 2, -time.Second},
			{"same checked time", 2, 0},
		} {
			t.Run(test.name+map[bool]string{false: "/fixed", true: "/plan"}[escalate], func(t *testing.T) {
				now := time.Now().UTC()
				account := &Account{ID: "account", modelAccessGeneration: 2, runtime: accountRuntimeState{
					Cooldowns: map[string]CooldownState{"shared": {Until: now.Add(time.Hour), Reason: "current", Level: 3}},
					ModelAccess: map[string]ModelAccess{
						"shared":          {CheckedAt: now},
						globalCooldownKey: {CheckedAt: now},
					},
				}}
				pool := NewAccountPool([]*Account{account}, 1)
				before := cloneRuntime(account.runtime)
				until, err := pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", test.generation, now.Add(test.delay), QuotaCooldown{
					Until: now.Add(2 * time.Hour), Reason: "stale", Escalate: escalate,
				})
				if err != nil || !until.IsZero() || !reflect.DeepEqual(account.runtime, before) {
					t.Fatalf("until=%v err=%v runtime=%+v want=%+v", until, err, account.runtime, before)
				}
				if test.delay != 0 {
					if _, err := pool.MarkModelAccessVerifiedIfGeneration(account.ID, "shared", test.generation, now.Add(test.delay)); err != nil {
						t.Fatal(err)
					}
					if err := pool.ClearCooldownIfGeneration(account.ID, "shared", test.generation, now.Add(test.delay)); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(account.runtime, before) {
						t.Fatalf("stale success changed runtime=%+v want=%+v", account.runtime, before)
					}
				}
			})
		}
	}
}

func TestQuotaCooldownSuccessResetsLevel(t *testing.T) {
	for _, verify := range []bool{false, true} {
		t.Run(map[bool]string{false: "clear", true: "verified"}[verify], func(t *testing.T) {
			now := time.Now().UTC()
			account := &Account{ID: "account", modelAccessGeneration: 1, runtime: accountRuntimeState{
				Cooldowns: map[string]CooldownState{
					"shared":          {Until: now.Add(-time.Second), Reason: "previous plan", Level: 2},
					"build:shared":    {Until: now.Add(time.Hour), Reason: "other channel", Level: 4},
					globalCooldownKey: {Until: now.Add(time.Hour), Reason: "global", Level: 1},
				},
			}}
			pool := NewAccountPool([]*Account{account}, 1)
			cooldown := QuotaCooldown{Until: now.Add(time.Minute), Reason: "next plan", Escalate: true}
			until, err := pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, now, cooldown)
			if err != nil || until.Before(now.Add(15*time.Minute)) || until.After(time.Now().Add(15*time.Minute)) ||
				account.runtime.Cooldowns["shared"].Level != 3 || !until.Equal(account.runtime.Cooldowns["shared"].Until) {
				t.Fatalf("expired plan did not escalate: until=%v err=%v state=%+v", until, err, account.runtime.Cooldowns["shared"])
			}
			checkedAt := now.Add(time.Second)
			if verify {
				if _, err := pool.MarkModelAccessVerifiedIfGeneration(account.ID, "shared", 1, checkedAt); err != nil {
					t.Fatal(err)
				}
			} else if err := pool.ClearCooldownIfGeneration(account.ID, "shared", 1, checkedAt); err != nil {
				t.Fatal(err)
			}
			if len(account.runtime.Cooldowns) != 1 || account.runtime.Cooldowns["build:shared"].Level != 4 {
				t.Fatalf("success must clear only matching and global cooldowns: %+v", account.runtime.Cooldowns)
			}
			until, err = pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, now, cooldown)
			if err != nil || !until.IsZero() || len(account.runtime.Cooldowns) != 1 {
				t.Fatalf("old error restored cooldown: until=%v err=%v state=%+v", until, err, account.runtime.Cooldowns)
			}
			until, err = pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, checkedAt.Add(time.Second), cooldown)
			if err != nil || until.IsZero() || account.runtime.Cooldowns["shared"].Level != 1 {
				t.Fatalf("success did not reset level: until=%v err=%v state=%+v", until, err, account.runtime.Cooldowns["shared"])
			}
		})
	}
}

func TestQuotaCooldownPersistence(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, runtimeStateName)
	if err := os.WriteFile(path, []byte(`{"cooldowns":{"shared":{"until":"2000-01-01T00:00:00Z","reason":"legacy"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := readRuntime(path)
	if err != nil || legacy.Cooldowns["shared"].Level != 0 || legacy.Cooldowns["shared"].Reason != "legacy" {
		t.Fatalf("legacy runtime=%+v err=%v", legacy, err)
	}
	account := &Account{ID: "account", Directory: directory, RuntimePath: path, modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 1)
	now := time.Now().UTC()
	cooldown := QuotaCooldown{Until: now.Add(time.Minute), Reason: "plan limit", Escalate: true}
	until, err := pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, now, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := readRuntime(path)
	if err != nil || persisted.Cooldowns["shared"].Level != 1 || !persisted.Cooldowns["shared"].Until.Equal(until) {
		t.Fatalf("persisted runtime=%+v err=%v", persisted, err)
	}
	previous := persisted.Cooldowns["shared"]
	previous.Until = now.Add(-time.Second)
	persisted.Cooldowns["shared"] = previous
	if err := writeRuntime(path, persisted); err != nil {
		t.Fatal(err)
	}
	reloaded := &Account{ID: account.ID, Directory: directory, RuntimePath: path, modelAccessGeneration: 1}
	pool = NewAccountPool([]*Account{reloaded}, 1)
	until, err = pool.MarkQuotaCooldownIfGeneration(reloaded.ID, "shared", 1, now.Add(time.Second), cooldown)
	if err != nil || until.Before(now.Add(5*time.Minute)) || until.After(time.Now().Add(5*time.Minute)) {
		t.Fatalf("reloaded until=%v err=%v", until, err)
	}
	persisted, err = readRuntime(path)
	if err != nil || persisted.Cooldowns["shared"].Level != 2 || !persisted.Cooldowns["shared"].Until.Equal(until) {
		t.Fatalf("reloaded runtime=%+v err=%v", persisted, err)
	}
}

func TestQuotaCooldownWritersPreserveScope(t *testing.T) {
	for _, writer := range []string{"retryable failure", "stream", "bidi"} {
		for _, channel := range []Channel{ChannelPlayground, ChannelBuild} {
			for _, global := range []bool{false, true} {
				t.Run(writer+"/"+string(channel)+map[bool]string{false: "/model", true: "/global"}[global], func(t *testing.T) {
					model := Model{ID: "shared", Methods: []string{"generateContent"}}
					account := &Account{ID: "account", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1}
					pool := NewAccountPool([]*Account{account}, 1)
					pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
					if err := pool.SetBuildCatalog(account.ID, []Model{model}); err != nil {
						t.Fatal(err)
					}
					ctx := ContextWithChannel(context.Background(), channel)
					selection := AccountSelection{ModelID: model.ID, Method: "generateContent"}
					lease, _, err := pool.TryAcquireFor(ctx, selection)
					if err != nil || lease == nil {
						t.Fatalf("lease=%v err=%v", lease, err)
					}
					t.Cleanup(func() {
						if err := lease.Release(); err != nil {
							t.Error(err)
						}
					})
					scope := lease.CooldownScope(model.ID)
					rpcError := &RPCError{StatusCode: http.StatusTooManyRequests, Message: "You have reached your usage limit"}
					if global {
						scope = globalCooldownKey
						rpcError.Metadata = map[string]string{"quota_metric": "requests_global"}
					}
					account.runtime.Cooldowns[scope] = CooldownState{Until: time.Now().Add(-time.Second), Level: 1}
					before := time.Now()
					switch writer {
					case "retryable failure":
						service := &PooledService{pool: pool}
						if err := service.markRetryableFailure(lease, lease.CooldownScope(model.ID), rpcError); err != nil {
							t.Fatal(err)
						}
					case "stream":
						source, destination := make(chan Event, 1), make(chan Event, 1)
						source <- Event{Kind: EventError, Err: rpcError}
						close(source)
						forwardEventsWithLease(ctx, source, destination, lease, pool, model.ID)
						if event := <-destination; event.Kind != EventError || !errors.Is(event.Err, rpcError) {
							t.Fatalf("event=%+v", event)
						}
					case "bidi":
						session := &BidiSession{lease: lease, accountID: account.ID, model: model.ID, modelAccessScope: model.ID}
						session.beginQualificationAttempt()
						event := BidiEvent{Kind: BidiEventError, Err: rpcError}
						session.recordScopedModelAccess(&event)
						if !errors.Is(event.Err, rpcError) {
							t.Fatalf("event=%+v", event)
						}
					}
					current := account.runtime.Cooldowns[scope]
					if len(account.runtime.Cooldowns) != 1 || current.Level != 2 || current.Until.Before(before.Add(5*time.Minute)) ||
						current.Until.After(time.Now().Add(5*time.Minute)) {
						t.Fatalf("writer lost escalation or scope: %+v", account.runtime.Cooldowns)
					}
					if err := lease.Release(); err != nil {
						t.Fatal(err)
					}
					for _, candidate := range []Channel{ChannelPlayground, ChannelBuild} {
						ctx := ContextWithChannel(context.Background(), candidate)
						next, waitable, err := pool.TryAcquireFor(ctx, selection)
						if global || candidate == channel {
							if next != nil || !waitable || err != nil {
								t.Fatalf("cooled channel must not fall back: channel=%s lease=%v waitable=%v err=%v", candidate, next, waitable, err)
							}
						} else {
							if err != nil || next == nil || next.Channel() != candidate {
								t.Fatalf("other channel blocked: channel=%s lease=%v err=%v", candidate, next, err)
							}
							if err := next.Release(); err != nil {
								t.Fatal(err)
							}
						}
					}
				})
			}
		}
	}
}

func TestQuotaCooldownExplicitRetry(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: "account", modelAccessGeneration: 1, runtime: accountRuntimeState{
		Cooldowns: map[string]CooldownState{"shared": {Until: now.Add(time.Hour), Level: 5}},
	}}
	pool := NewAccountPool([]*Account{account}, 1)
	cooldown, ok := QuotaCooldownForError(&RPCError{
		StatusCode: http.StatusTooManyRequests, Message: "You have reached your usage limit", RetryDelay: 17 * time.Second,
	}, now)
	if !ok || cooldown.Escalate {
		t.Fatalf("cooldown=%+v ok=%v", cooldown, ok)
	}
	until, err := pool.MarkQuotaCooldownIfGeneration(account.ID, "shared", 1, now, cooldown)
	if err != nil || !until.Equal(now.Add(17*time.Second)) || !account.runtime.Cooldowns["shared"].Until.Equal(until) ||
		account.runtime.Cooldowns["shared"].Level != 0 {
		t.Fatalf("explicit delay changed: until=%v err=%v state=%+v", until, err, account.runtime.Cooldowns["shared"])
	}
}

func TestQuotaRetryAt(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, delay := range []time.Duration{-time.Second, 0, 1500 * time.Millisecond} {
		err := &RPCError{StatusCode: http.StatusTooManyRequests, RetryDelay: delay}
		want := time.Time{}
		if delay > 0 {
			want = now.Add(delay)
		}
		if got := err.RetryAt(now); !got.Equal(want) {
			t.Fatalf("delay=%v retry at=%v want=%v", delay, got, want)
		}
	}
	err := &AllCoolingError{ModelID: "shared", Until: now.Add(time.Minute)}
	if got := err.RetryAt(now.Add(time.Hour)); !got.Equal(err.Until) {
		t.Fatalf("retry at=%v want=%v", got, err.Until)
	}
}
