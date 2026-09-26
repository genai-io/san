// Package trigger handles Source 3 (system → agent) inputs:
// cron scheduled prompts, async hook rewakes, and file watcher events.
package trigger

import (
	"sync"
)

type Model struct {
	CronQueue      []string
	AsyncHookQueue *AsyncHookQueue
	FileWatcher    *FileWatcher
}

func New() Model {
	return Model{
		AsyncHookQueue: &AsyncHookQueue{},
	}
}

// AsyncHookRewake holds data for an async hook continuation.
type AsyncHookRewake struct {
	Notice             string
	Context            []string
	ContinuationPrompt string
}

// AsyncHookQueue is a thread-safe queue for async hook rewake items.
type AsyncHookQueue struct {
	mu    sync.Mutex
	items []AsyncHookRewake
}

func (q *AsyncHookQueue) Push(item AsyncHookRewake) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
}

func (q *AsyncHookQueue) Pop() (AsyncHookRewake, bool) {
	if q == nil {
		return AsyncHookRewake{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return AsyncHookRewake{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

func (q *AsyncHookQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
