package aistudio

import (
	"context"
	"testing"
	"time"
)

func TestAccountRuntimeUpdateWindow(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	account := &Account{ID: "fixture", Config: AccountConfig{Enabled: true}, State: AccountReady,
		Models: []Model{{ID: "model", Methods: []string{"generateContent"}}}, modelAccessGeneration: 1}
	pool := NewAccountPool([]*Account{account}, 2)
	selection := AccountSelection{AccountID: account.ID, ModelID: "model", Method: "generateContent"}
	lease, err := pool.AcquireFor(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	other, err := pool.AcquireFor(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if end, ok := lease.BeginRuntimeUpdate(); ok || end != nil {
		t.Fatal("runtime update accepted another active request")
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
	authEnd, ok := lease.BeginAuthRefresh()
	if !ok {
		t.Fatal("could not reserve authentication refresh")
	}
	if end, ok := lease.BeginRuntimeUpdate(); ok || end != nil {
		t.Fatal("runtime update overlapped an authentication refresh")
	}
	authEnd()
	changed := pool.Changed()
	end, ok := lease.BeginRuntimeUpdate()
	if !ok {
		t.Fatal("sole active request could not reserve runtime update")
	}
	defer end()
	select {
	case <-changed:
	default:
		t.Fatal("runtime reservation did not notify pool waiters")
	}
	if nested, ok := lease.BeginRuntimeUpdate(); ok || nested != nil {
		t.Fatal("overlapping runtime maintenance was accepted")
	}
	if next, waitable, err := pool.TryAcquireFor(ctx, selection); err != nil || next != nil || !waitable {
		t.Fatalf("new request entered runtime maintenance: lease=%v waitable=%t err=%v", next, waitable, err)
	}
	end()
	changed = pool.Changed()
	end()
	select {
	case <-changed:
		t.Fatal("ending runtime maintenance twice changed pool state")
	default:
	}
	next, _, err := pool.TryAcquireFor(ctx, selection)
	if err != nil || next == nil {
		t.Fatalf("runtime maintenance did not unblock requests: lease=%v err=%v", next, err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if end, ok := lease.BeginRuntimeUpdate(); ok || end != nil {
		t.Fatal("released lease reserved runtime maintenance")
	}
	exclusive, err := pool.AcquireAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Release()
	if finish, ok := exclusive.BeginRuntimeUpdate(); !ok {
		t.Fatal("exclusive lease could not update its runtime")
	} else {
		finish()
		finish()
	}
}

func TestInvalidLeaseCannotUpdateRuntime(t *testing.T) {
	for _, lease := range []*AccountLease{nil, {}} {
		if end, ok := lease.BeginRuntimeUpdate(); ok || end != nil {
			t.Fatal("invalid lease reserved runtime maintenance")
		}
	}
}
