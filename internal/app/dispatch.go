package app

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue 按账户选择条件分组，同条件的请求按到达先后取号
type dispatchQueue struct {
	mu      sync.Mutex
	pool    *aistudio.AccountPool
	waiters []*dispatchWaiter
}

// dispatchWaiter 表示一个正在取号的请求
type dispatchWaiter struct {
	ctx       context.Context
	selection aistudio.AccountSelection
	wake      chan struct{}
	// starting 表示请求已取得账户、正在等它的 Worker 启动，排在后面的请求不必等它
	starting bool
}

func newDispatchQueue(pool *aistudio.AccountPool) *dispatchQueue {
	return &dispatchQueue{pool: pool}
}

// dispatchKey 返回可互相替代的请求共用的队列键
func dispatchKey(selection aistudio.AccountSelection) string {
	allowed := slices.Clone(selection.AllowedAccountIDs)
	slices.Sort(allowed)
	return strings.Join([]string{
		selection.ModelID, selection.ModelAccessScope, selection.Method, selection.Capability, selection.AccountID, selection.ResourceID,
		strings.Join(allowed, ","), strconv.FormatBool(selection.AllowedAccountIDs != nil),
		strconv.FormatBool(selection.PlaygroundOnly), string(selection.Channel), strconv.FormatBool(selection.IgnoreQuotaCooldown),
	}, "\x00")
}

// join 把请求排到同条件队列末尾
func (queue *dispatchQueue) join(ctx context.Context, selection aistudio.AccountSelection) *dispatchWaiter {
	selection.AllowedAccountIDs = slices.Clone(selection.AllowedAccountIDs)
	waiter := &dispatchWaiter{ctx: ctx, selection: selection, wake: make(chan struct{}, 1)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.waiters = append(queue.waiters, waiter)
	return waiter
}

// turn compares current scopes in arrival order, including after live key updates.
func (queue *dispatchQueue) turn(waiter *dispatchWaiter, selection aistudio.AccountSelection) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	selection.AllowedAccountIDs = slices.Clone(selection.AllowedAccountIDs)
	waiter.selection = selection
	keyFor := queue.keySnapshot()
	key := keyFor(waiter)
	for _, ahead := range queue.waiters {
		if ahead == waiter {
			return true
		}
		if !ahead.starting && keyFor(ahead) == key {
			return false
		}
	}
	return false
}

// setStarting 记录请求是否在等 Worker 启动并唤醒随之轮到取号的请求
func (queue *dispatchQueue) setStarting(waiter *dispatchWaiter, starting bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	waiter.starting = starting
	queue.wakeHeadsLocked()
}

// leave 移除请求并唤醒随之轮到取号的请求
func (queue *dispatchQueue) leave(waiter *dispatchWaiter) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	index := slices.Index(queue.waiters, waiter)
	queue.waiters = slices.Delete(queue.waiters, index, index+1)
	queue.wakeHeadsLocked()
}

// wakeHeads 在账户或 Worker 状态变化后唤醒每个队列中轮到取号的请求
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.wakeHeadsLocked()
}

func (queue *dispatchQueue) keySnapshot() func(*dispatchWaiter) string {
	scope := queue.pool.ScopedSelectionSnapshot()
	return func(waiter *dispatchWaiter) string {
		return dispatchKey(scope(waiter.ctx, waiter.selection))
	}
}

// wakeHeadsLocked wakes the first request in each current scope without reordering.
func (queue *dispatchQueue) wakeHeadsLocked() {
	if len(queue.waiters) == 0 {
		return
	}
	keyFor := queue.keySnapshot()
	seen := make(map[string]struct{})
	for _, waiter := range queue.waiters {
		if !waiter.starting {
			key := keyFor(waiter)
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				waiter.signal()
			}
		}
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
