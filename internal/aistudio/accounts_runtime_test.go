package aistudio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
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

func TestRetainedAccountLeaseDefersCapacityRelease(t *testing.T) {
	account := &Account{ID: "fixture", Config: AccountConfig{Enabled: true}, State: AccountReady}
	pool := NewAccountPool([]*Account{account}, 1)
	selection := AccountSelection{AccountID: account.ID}
	lease, err := pool.AcquireFor(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	first, second := lease.Retain(), lease.Retain()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first(); err != nil {
		t.Fatal(err)
	}
	if next, waitable, err := pool.TryAcquireFor(t.Context(), selection); err != nil || next != nil || !waitable {
		t.Fatalf("retained lease released its account early: lease=%v waitable=%t err=%v", next, waitable, err)
	}
	if err := second(); err != nil {
		t.Fatal(err)
	}
	next, _, err := pool.TryAcquireFor(t.Context(), selection)
	if err != nil || next == nil {
		t.Fatalf("final retain did not release account capacity: lease=%v err=%v", next, err)
	}
	defer next.Release()
}

func TestRestrictionSurvivesAuthenticationAndEnableChanges(t *testing.T) {
	model := Model{ID: "model", Methods: []string{"generateContent"}}
	directory := t.TempDir()
	account := &Account{ID: "account", Directory: directory, ConfigPath: filepath.Join(directory, "account.json"),
		StoragePath: filepath.Join(directory, StorageStateName), RuntimePath: filepath.Join(directory, runtimeStateName),
		Config: AccountConfig{Label: "account@example.com", Enabled: true, Locale: "en-US", Timezone: "UTC"}, State: AccountReady, Models: []Model{model}}
	if err := writeAccountConfig(account.ConfigPath, account.Config); err != nil {
		t.Fatal(err)
	}
	if err := WriteStorageState(account.StoragePath, StorageState{}); err != nil {
		t.Fatal(err)
	}
	pool := NewAccountPool([]*Account{account}, 1)
	lease, err := pool.AcquireFor(t.Context(), AccountSelection{AccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	denial := camoufoxnative.DenialForMessage("Account not supported.")
	if err := pool.MarkRestricted(account.ID, denial); err != nil {
		t.Fatal(err)
	}
	if persisted, err := readRuntime(account.RuntimePath); err != nil || persisted.Restricted != denial.Error() {
		t.Fatalf("restriction was not persisted: state=%+v err=%v", persisted, err)
	}
	for _, enabled := range []bool{true, false, true} {
		if _, err := pool.SetEnabled(account.ID, enabled); err != nil {
			t.Fatal(err)
		}
		want := AccountRestricted
		if !enabled {
			want = AccountDisabled
		}
		for _, update := range []func() error{
			func() error { return pool.MarkReady(account.ID) },
			func() error { return pool.MarkAuthRequired(account.ID, "expired session") },
			func() error { return lease.markAuthenticationStateAt(true, "expired session", time.Now()) },
			func() error { return lease.markAuthenticationStateAt(false, "", time.Now()) },
		} {
			if err := update(); err != nil {
				t.Fatal(err)
			}
			if account.State != want || pool.RestrictedReason(account.ID) != denial.Error() {
				t.Fatalf("authentication bypassed persistent restriction: enabled=%v state=%s reason=%q", enabled, account.State, pool.RestrictedReason(account.ID))
			}
		}
	}
	if err := pool.SetCatalog(account.ID, BenefitTierFree, []Model{model}); err != nil {
		t.Fatal(err)
	}
	if pool.RestrictedReason(account.ID) != "" || account.State != AccountReady {
		t.Fatal("successful catalog refresh did not remove the restriction")
	}
	if pool.RestrictedReason("missing") != "" || RestrictionDenial(camoufoxnative.DenialForMessage("Region not supported.")) != nil {
		t.Fatal("unknown accounts or regional failures became permanent restrictions")
	}
}

func TestConcurrentRestrictedImportsPreserveAllRecords(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, restrictedImportsName), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewAccountStore(directory)
	const count = 24
	start := make(chan struct{})
	failures := make(chan error, count)
	var writers sync.WaitGroup
	for index := range count {
		writers.Go(func() {
			<-start
			failures <- store.RecordRestrictedImport(fmt.Sprintf(" User%d@Example.COM ", index), fmt.Sprintf("reason %d", index))
		})
	}
	close(start)
	writers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.RestrictedImports()
	if err != nil || len(records) != count {
		t.Fatalf("concurrent import records lost: count=%d err=%v", len(records), err)
	}
	for index := range count {
		if records[fmt.Sprintf("user%d@example.com", index)] != fmt.Sprintf("reason %d", index) {
			t.Fatalf("missing restriction for account %d", index)
		}
	}
}
