package camoufoxnative

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAccountCacheRetainsLockAndActiveContents(t *testing.T) {
	storage := filepath.Join(t.TempDir(), "storage-state.json")
	if err := RemoveAccountCache(storage); err != nil {
		t.Fatal(err)
	}
	directory, lock, err := lockAccountCache(storage)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = releaseAccountCache(lock) })
	path := filepath.Join(directory, "entry")
	if err := os.WriteFile(path, []byte("active-cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(directory, ".lock")
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccountCache(storage); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "active-cache" {
		t.Fatal("cleanup changed a live browser's cache")
	}
	if err := releaseAccountCache(lock); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccountCache(storage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("inactive cache was not removed: %v", err)
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("cleanup removed the shared cache lock inode")
	}
	_, next, err := lockAccountCache(storage)
	if err != nil || next == nil {
		t.Fatalf("cleanup did not release its cache lock: %v", err)
	}
	if err := releaseAccountCache(next); err != nil {
		t.Fatal(err)
	}
}

func TestCopyStartupFileStreamsAndTruncates(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "source"), filepath.Join(root, "nested", "target")
	data := bytes.Repeat([]byte("startup-cache\x00"), 100000)
	if err := os.WriteFile(from, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{data, []byte("shorter")} {
		if err := os.WriteFile(from, want, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := copyStartupFile(from, to); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(to); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("copied cache differs: %v", err)
		}
	}
}
