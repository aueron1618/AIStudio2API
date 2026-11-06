package app

import "context"

// AccountTaskContext outlives HTTP requests but is canceled before stopping or replacing a runtime.
func (manager *runtimeManager) AccountTaskContext() context.Context {
	manager.taskMu.Lock()
	defer manager.taskMu.Unlock()
	if manager.taskContext == nil {
		manager.taskContext, manager.taskCancel = context.WithCancel(manager.lifecycle)
		if manager.taskTransitions != 0 {
			manager.taskCancel()
		}
	}
	return manager.taskContext
}

// cancelAccountTasks blocks new tasks until every overlapping transition finishes.
// Close deliberately retains its transition so the manager cannot accept more tasks.
func (manager *runtimeManager) cancelAccountTasks() func() {
	manager.taskMu.Lock()
	defer manager.taskMu.Unlock()
	manager.taskTransitions++
	if manager.taskCancel != nil {
		manager.taskCancel()
	}
	return func() {
		manager.taskMu.Lock()
		defer manager.taskMu.Unlock()
		manager.taskTransitions--
		if manager.taskTransitions == 0 {
			manager.taskContext, manager.taskCancel = nil, nil
		}
	}
}
