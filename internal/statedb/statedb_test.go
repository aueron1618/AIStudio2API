package statedb

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestStateRoundTripAndLoadRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "states.db")
	store, err := Open(path, func(_, message string) { t.Error(message) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.Save([]api.SavedState{{ID: "response", Data: []byte(`{"text":"old"}`)}, {ID: "removed", Data: []byte(`{}`)}}, nil)
	store.Save([]api.SavedState{{ID: "interaction", Data: []byte(`{"text":"next"}`)}, {ID: "response", Data: []byte(`{"text":"updated"}`)}}, []string{"removed"})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, func(_, message string) { t.Error(message) })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	want := []api.SavedState{{ID: "response", Data: []byte(`{"text":"updated"}`)}, {ID: "interaction", Data: []byte(`{"text":"next"}`)}}
	if got := reopened.Load(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored states or original ordering changed: %+v", got)
	}
	if reopened.loaded != nil || reopened.Load() != nil {
		t.Fatal("startup state snapshot remained retained after hydration")
	}
}

func TestStateSaveFailureRollsBack(t *testing.T) {
	var warnings []string
	store, err := Open(filepath.Join(t.TempDir(), "states.db"), func(level, message string) {
		warnings = append(warnings, level+": "+message)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := []api.SavedState{{ID: "response", Data: []byte(`{"text":"original"}`)}, {ID: "keep", Data: []byte(`{}`)}}
	if err := store.save(want, nil); err != nil {
		t.Fatal(err)
	}
	invalid := []api.SavedState{{ID: "response", Data: []byte(`{"text":"fixture-private-body"}`)}, {ID: "invalid", Data: nil}}
	if err := store.save(invalid, []string{"keep"}); err == nil {
		t.Fatal("NULL state unexpectedly passed the storage constraint")
	}
	store.Save(invalid, []string{"keep"})
	got, err := readStates(store.db)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed save changed existing states: %+v, %v", got, err)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "WARN:") || strings.Contains(warnings[0], "fixture-private-body") {
		t.Fatalf("storage failure did not produce a safe warning: %v", warnings)
	}
}
