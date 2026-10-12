package waa

import (
	"sync"
	"testing"
	"time"
)

func TestEventLoopReleasesShortTimers(t *testing.T) {
	loop := newEventLoop()
	defer loop.close()
	var pending sync.WaitGroup
	for range 1000 {
		pending.Add(1)
		loop.schedule(time.Nanosecond, pending.Done)
	}
	done := make(chan struct{})
	go func() { pending.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timer callbacks did not complete")
	}
	loop.mu.Lock()
	remaining := len(loop.timers)
	loop.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("retained %d expired timers", remaining)
	}
}

func TestEventLoopCloseDropsPendingWork(t *testing.T) {
	loop := newEventLoop()
	loop.schedule(time.Hour, func() { t.Error("closed timer fired") })
	loop.close()
	loop.schedule(time.Hour, func() { t.Error("scheduled after close") })
	if loop.post(func() {}) {
		t.Fatal("closed loop accepted a job")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if len(loop.timers) != 0 || len(loop.queue) != 0 {
		t.Fatal("closed loop retained pending work")
	}
}
