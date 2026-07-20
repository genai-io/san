package task

import (
	"sync"
)

// LifecycleHandler receives task lifecycle notifications. The app layer
// installs a handler that fires hooks, updates the tracker, and publishes
// events to the Hub.
type LifecycleHandler interface {
	TaskCreated(info TaskInfo)
	TaskCompleted(info TaskInfo)
}

var taskHandler struct {
	mu      sync.RWMutex
	handler LifecycleHandler
}

// SetLifecycleHandler installs or clears the task lifecycle handler.
func SetLifecycleHandler(h LifecycleHandler) {
	taskHandler.mu.Lock()
	defer taskHandler.mu.Unlock()
	taskHandler.handler = h
}

// notifyTaskCreated announces a task the manager has just taken ownership of.
//
// Callers must release the manager lock first, as notifyTaskCompleted's callers
// already do. The handler builds the task's tracker entry, so this reaches the
// todo store's lock — and that store takes the manager's lock in the other
// direction when it reconciles orphaned entries against live tasks. Notifying
// under the manager lock closes that cycle into a deadlock.
func notifyTaskCreated(info TaskInfo) {
	taskHandler.mu.RLock()
	h := taskHandler.handler
	taskHandler.mu.RUnlock()
	if h != nil {
		h.TaskCreated(info)
	}
}

func notifyTaskCompleted(info TaskInfo) {
	taskHandler.mu.RLock()
	h := taskHandler.handler
	taskHandler.mu.RUnlock()
	if h != nil {
		h.TaskCompleted(info)
	}
}
