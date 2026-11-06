package aistudio

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// operationContext preserves caller deadlines while also observing page closure.
func (runtime *goWAARuntime) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(runtime.ctx, cancel)
	if runtime.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func (runtime *goWAARuntime) beginHTTP(ctx context.Context) (context.Context, context.CancelFunc, func(), error) {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if runtime.closed {
		return nil, nil, nil, errors.New("SpiderMonkey WAA runtime is closed")
	}
	runtime.pending.Add(1)
	ctx, cancel := runtime.operationContext(ctx)
	return ctx, cancel, runtime.pending.Done, nil
}

// schedule owns both the timer and its bounded callback, including shutdown races.
func (runtime *goWAARuntime) schedule(delay time.Duration, task func(context.Context)) *time.Timer {
	runtime.lifecycleMu.Lock()
	defer runtime.lifecycleMu.Unlock()
	if runtime.closed {
		return nil
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		runtime.lifecycleMu.Lock()
		delete(runtime.timers, timer)
		if runtime.closed {
			runtime.lifecycleMu.Unlock()
			return
		}
		runtime.pending.Add(1)
		runtime.lifecycleMu.Unlock()
		defer runtime.pending.Done()
		ctx, cancel := context.WithTimeout(runtime.ctx, 2*time.Minute)
		defer cancel()
		task(ctx)
	})
	runtime.timers[timer] = struct{}{}
	return timer
}

func (runtime *goWAARuntime) stopTimer(timer *time.Timer) {
	runtime.lifecycleMu.Lock()
	timer.Stop()
	delete(runtime.timers, timer)
	runtime.lifecycleMu.Unlock()
}

type pageResponseBody struct {
	io.ReadCloser
	runtime *goWAARuntime
	cancel  context.CancelFunc
	finish  func()
	once    sync.Once
	err     error
}

func (body *pageResponseBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if err != nil {
		_ = body.Close()
	}
	return n, err
}

func (body *pageResponseBody) Close() error {
	body.once.Do(func() {
		body.cancel()
		body.err = body.ReadCloser.Close()
		body.runtime.lifecycleMu.Lock()
		delete(body.runtime.requests, body)
		body.runtime.lifecycleMu.Unlock()
		body.finish()
	})
	return body.err
}
