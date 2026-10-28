package camoufoxnative

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoginResultKeepsExportOnCleanupFailure(t *testing.T) {
	loginErr := errors.New("login failed")
	closeErr := errors.New("tree cleanup failed")
	for _, test := range []struct {
		name  string
		err   error
		close error
	}{
		{"success", nil, nil},
		{"cleanup-warning", nil, closeErr},
		{"failure", loginErr, nil},
		{"failure-with-cleanup", loginErr, closeErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := LoginResult{StorageStateJSON: []byte(`{"cookies":[]}`), Email: "real@example.com"}
			err := result.recordClose(test.err, test.close)
			if test.err == nil {
				if err != nil || result.CloseError != errorText(test.close) || result.Email != "real@example.com" || len(result.StorageStateJSON) == 0 {
					t.Fatal("successful export was lost to cleanup failure")
				}
			} else if !errors.Is(err, loginErr) || test.close != nil && !errors.Is(err, closeErr) || result.CloseError != "" {
				t.Fatalf("login failure lost its primary or cleanup error: %v", err)
			}
		})
	}
}

func TestBrowserCloseAlwaysTerminatesTreeBeforeProfileRemoval(t *testing.T) {
	profile := t.TempDir()
	process := &browserProcess{profile: profile, done: make(chan struct{})}
	close(process.done)
	calls := 0
	terminate := func(ctx context.Context, got *browserProcess) error {
		calls++
		deadline, ok := ctx.Deadline()
		if got != process || !ok || time.Until(deadline) > browserProcessCloseTimeout {
			t.Fatal("process tree cleanup lost its deadline or target")
		}
		if _, err := os.Stat(profile); err != nil {
			t.Fatal("profile was removed before process tree cleanup")
		}
		return nil
	}
	if err := process.close(browserProcessCloseTimeout, terminate); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !process.closed {
		t.Fatal("an exited parent skipped process tree cleanup")
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("profile was not removed")
	}
	if err := process.close(browserProcessCloseTimeout, terminate); err != nil || calls != 1 {
		t.Fatal("successful Close was not idempotent")
	}
}
