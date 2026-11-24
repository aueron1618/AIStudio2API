package statedb

import (
	"bytes"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestStateRoundTripAndLoadRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "states.db")
	store, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.Save([]api.SavedState{{ID: "response", Data: []byte(`{"text":"old"}`)}, {ID: "removed", Data: []byte(`{}`)}}, nil)
	store.Save([]api.SavedState{{ID: "interaction", Data: []byte(`{"text":"next"}`)}, {ID: "response", Data: []byte(`{"text":"updated"}`)}}, []string{"removed"})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	want := []api.SavedState{{ID: "response", Data: []byte(`{"text":"updated"}`)}, {ID: "interaction", Data: []byte(`{"text":"next"}`)}}
	if got := storedStates(t, reopened); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored states or original ordering changed: %+v", got)
	}
	if got := reopened.Index(); !reflect.DeepEqual(got, []api.StateNode{{ID: "response"}, {ID: "interaction"}}) {
		t.Fatalf("restored node index changed: %+v", got)
	}
	if reopened.index != nil || reopened.Index() != nil {
		t.Fatal("startup state snapshot remained retained after hydration")
	}
}

func TestStateSaveFailureRollsBack(t *testing.T) {
	var warnings []string
	store, err := Open(filepath.Join(t.TempDir(), "states.db"), func(level, message string, attrs ...any) {
		warnings = append(warnings, fmt.Sprintf("%s: %s %v", level, message, attrs))
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
	got := storedStates(t, store)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failed save changed existing states: %+v", got)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "WARN:") || strings.Contains(warnings[0], "fixture-private-body") {
		t.Fatalf("storage failure did not produce a safe warning: %v", warnings)
	}
}

func storedStates(t *testing.T, store *Store) []api.SavedState {
	t.Helper()
	index, err := readIndex(store.db)
	if err != nil {
		t.Fatal(err)
	}
	var states []api.SavedState
	for _, node := range index {
		data, ok := store.Read(node.ID)
		if !ok {
			t.Fatalf("node %q disappeared", node.ID)
		}
		states = append(states, api.SavedState{ID: node.ID, ParentID: node.ParentID, Data: data})
	}
	return states
}

func legacyStateDatabase(t *testing.T, saved []api.SavedState) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "states.db")
	db, err := sql.Open("sqlite3_aistudio2api", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE states (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, data BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, state := range saved {
		if _, err := db.Exec(`INSERT INTO states (seq, id, data) VALUES (?, ?, ?)`, i*3+2, state.ID, state.Data); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLegacyStateMigrationPreservesPayloadParentsAndSequence(t *testing.T) {
	want := []api.SavedState{
		{ID: "root", Data: []byte("{\n  \"ParentID\": \"\", \"Contents\": [], \"Record\": {\"model\": \"fixture\"}\n}")},
		{ID: "child", ParentID: "root", Data: []byte(`{"ParentID":"root","Contents":[{"role":"user","parts":[{"text":"fixture"}]}]}`)},
	}
	path := legacyStateDatabase(t, want)
	store, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err != nil {
		t.Fatal(err)
	}
	if got := storedStates(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy migration changed IDs, payloads or parents: %+v", got)
	}
	for i, state := range want {
		var seq int64
		if err := store.db.QueryRow(`SELECT seq FROM nodes WHERE id = ?`, state.ID).Scan(&seq); err != nil || seq != int64(i*3+2) {
			t.Fatalf("legacy sequence changed: %d, %v", seq, err)
		}
	}
	var exists bool
	if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'states')`).Scan(&exists); err != nil || exists {
		t.Fatalf("legacy table remained after successful conversion: %v, %v", exists, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := storedStates(t, reopened); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopening migrated nodes changed data: %+v", got)
	}
}

func TestLegacyStateMigrationFailureKeepsOldTable(t *testing.T) {
	want := []api.SavedState{
		{ID: "valid", Data: []byte(`{"ParentID":"","Contents":[]}`)},
		{ID: "invalid", Data: []byte(`{"ParentID":7,"private":"fixture-private-body"}`)},
	}
	path := legacyStateDatabase(t, want)
	store, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err == nil {
		store.Close()
		t.Fatal("invalid legacy payload unexpectedly migrated")
	}
	if strings.Contains(err.Error(), "fixture-private-body") {
		t.Fatal("migration error exposed state payload")
	}
	db, err := sql.Open("sqlite3_aistudio2api", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, state := range want {
		var data []byte
		if err := db.QueryRow(`SELECT data FROM states WHERE id = ?`, state.ID).Scan(&data); err != nil || !bytes.Equal(data, state.Data) {
			t.Fatalf("failed migration changed legacy state %q: %v", state.ID, err)
		}
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'nodes')`).Scan(&exists); err != nil || exists {
		t.Fatalf("failed migration left partial nodes: %v, %v", exists, err)
	}
}

func TestLegacyStateMigrationRejectsConflictingNodes(t *testing.T) {
	path := legacyStateDatabase(t, []api.SavedState{{ID: "same", Data: []byte(`{"ParentID":"","Contents":[]}`)}})
	db, err := sql.Open("sqlite3_aistudio2api", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO nodes (seq, id, parent, data) VALUES (2, 'same', '', ?)`, []byte(`{"Contents":["newer"]}`)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path, func(_, message string, attrs ...any) { t.Error(message, attrs) })
	if err == nil {
		store.Close()
		t.Fatal("conflicting node was overwritten")
	}
	db, err = sql.Open("sqlite3_aistudio2api", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM states`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("conflicting migration removed old states: %d, %v", count, err)
	}
}
