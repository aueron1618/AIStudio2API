package aistudio

import (
	"context"
	"sync"
)

// AssistantLifecycle owns assistant work for one service run, including detached cleanup.
type AssistantLifecycle struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	stopped bool
	pending sync.WaitGroup
}

type assistantLifecycleKey struct{}

// NewAssistantLifecycle creates a new generation; stopped generations cannot be reused.
func NewAssistantLifecycle(parent context.Context) *AssistantLifecycle {
	ctx, cancel := context.WithCancel(parent)
	return &AssistantLifecycle{ctx: ctx, cancel: cancel}
}

// ContextWithAssistantLifecycle captures the service generation at request admission.
func ContextWithAssistantLifecycle(ctx context.Context, lifecycle *AssistantLifecycle) context.Context {
	if lifecycle == nil {
		return ctx
	}
	return context.WithValue(ctx, assistantLifecycleKey{}, lifecycle)
}

func (lifecycle *AssistantLifecycle) begin(ctx context.Context) (context.Context, func(), error) {
	if lifecycle == nil {
		ctx, cancel := context.WithCancel(ctx)
		return ctx, cancel, nil
	}
	lifecycle.mu.Lock()
	if lifecycle.stopped || lifecycle.ctx.Err() != nil {
		lifecycle.mu.Unlock()
		return nil, nil, context.Canceled
	}
	lifecycle.pending.Add(1)
	lifecycle.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifecycle.ctx, cancel)
	if lifecycle.ctx.Err() != nil {
		cancel()
	}
	return ctx, sync.OnceFunc(func() {
		stop()
		cancel()
		lifecycle.pending.Done()
	}), nil
}

// Stop cancels and joins all accepted work before workers may be reset or closed.
func (lifecycle *AssistantLifecycle) Stop() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	lifecycle.stopped = true
	lifecycle.cancel()
	lifecycle.mu.Unlock()
	lifecycle.pending.Wait()
}
