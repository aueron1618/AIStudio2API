package app

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// dispatchQueue 按到达先后记录正在取号的请求，可用账户重叠的同条件请求按到达先后取用共同的账户
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

// dispatchKey 返回可互相替代的请求共用的条件键
func dispatchKey(selection aistudio.AccountSelection) string {
	return strings.Join([]string{
		selection.ModelID, selection.ModelAccessScope, selection.Method, selection.Capability, selection.ResourceID,
		strconv.FormatBool(selection.PlaygroundOnly), string(selection.Channel), strconv.FormatBool(selection.IgnoreQuotaCooldown),
		strconv.FormatBool(selection.AllowedAccountIDs != nil && len(selection.AllowedAccountIDs) == 0),
	}, "\x00")
}

// join captures admission context while preserving arrival order across live binding updates.
func (queue *dispatchQueue) join(ctx context.Context, selection aistudio.AccountSelection) *dispatchWaiter {
	selection.AllowedAccountIDs = slices.Clone(selection.AllowedAccountIDs)
	waiter := &dispatchWaiter{ctx: ctx, selection: selection, wake: make(chan struct{}, 1)}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.waiters = append(queue.waiters, waiter)
	return waiter
}

// turn reserves overlapping accounts for earlier equivalent requests using one live policy snapshot.
func (queue *dispatchQueue) turn(waiter *dispatchWaiter, selection aistudio.AccountSelection) (map[string]struct{}, bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	selection.AllowedAccountIDs = slices.Clone(selection.AllowedAccountIDs)
	waiter.selection = selection
	return queue.turnLocked(waiter, queue.selectionSnapshot())
}

func (queue *dispatchQueue) turnLocked(waiter *dispatchWaiter, scope func(*dispatchWaiter) aistudio.AccountSelection) (map[string]struct{}, bool) {
	selection := scope(waiter)
	key, accounts := dispatchKey(selection), dispatchAccounts(selection)
	var reserved map[string]struct{}
	for _, ahead := range queue.waiters {
		if ahead == waiter {
			break
		}
		if ahead.starting || ahead.ctx.Err() != nil {
			continue
		}
		selection := scope(ahead)
		aheadAccounts := dispatchAccounts(selection)
		if dispatchKey(selection) != key || !accountsOverlap(aheadAccounts, accounts) {
			continue
		}
		if aheadAccounts == nil {
			return nil, false
		}
		if reserved == nil {
			reserved = make(map[string]struct{})
		}
		for _, accountID := range aheadAccounts {
			reserved[accountID] = struct{}{}
		}
	}
	if len(reserved) > 0 && accounts != nil && !slices.ContainsFunc(accounts, func(accountID string) bool {
		_, taken := reserved[accountID]
		return !taken
	}) {
		return nil, false
	}
	return reserved, true
}

func dispatchAccounts(selection aistudio.AccountSelection) []string {
	if selection.AccountID != "" {
		if selection.AllowedAccountIDs != nil && !slices.Contains(selection.AllowedAccountIDs, selection.AccountID) {
			return []string{}
		}
		return []string{selection.AccountID}
	}
	return selection.AllowedAccountIDs
}

// accountsOverlap 判断两组可用账户是否有共同的账户，nil 表示全部账户
func accountsOverlap(left, right []string) bool {
	switch {
	case left == nil:
		return right == nil || len(right) > 0
	case right == nil:
		return len(left) > 0
	}
	return slices.ContainsFunc(left, func(accountID string) bool { return slices.Contains(right, accountID) })
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
	queue.waiters = slices.DeleteFunc(queue.waiters, func(other *dispatchWaiter) bool { return other == waiter })
	queue.wakeHeadsLocked()
}

// wakeHeads 在账户或 Worker 状态变化后唤醒全部轮到取号的请求
func (queue *dispatchQueue) wakeHeads() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.wakeHeadsLocked()
}

func (queue *dispatchQueue) selectionSnapshot() func(*dispatchWaiter) aistudio.AccountSelection {
	scope := queue.pool.ScopedSelectionSnapshot()
	return func(waiter *dispatchWaiter) aistudio.AccountSelection {
		return scope(waiter.ctx, waiter.selection)
	}
}

// wakeHeadsLocked wakes requests with unreserved accounts without reordering their live scopes.
func (queue *dispatchQueue) wakeHeadsLocked() {
	if len(queue.waiters) == 0 {
		return
	}
	scope := queue.selectionSnapshot()
	for _, waiter := range queue.waiters {
		if waiter.starting || waiter.ctx.Err() != nil {
			continue
		}
		if _, ok := queue.turnLocked(waiter, scope); ok {
			waiter.signal()
		}
	}
}

func (waiter *dispatchWaiter) signal() {
	select {
	case waiter.wake <- struct{}{}:
	default:
	}
}
