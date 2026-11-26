package tui

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

type asyncHookTickMsg struct{}

func startAsyncHookTicker() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return asyncHookTickMsg{} })
}

func (m *model) handleAsyncHookTick() tea.Cmd {
	next := startAsyncHookTicker()
	if !m.conv.Stream.Active {
		if item, ok := m.asyncHookQueue.pop(); ok {
			return tea.Batch(next, m.injectAsyncHookContinuation(item))
		}
	}
	return next
}

type asyncHookRewake struct {
	Notice             string
	Context            []string
	ContinuationPrompt string
}

// asyncHookQueue is a thread-safe queue for async hook rewake items.
type asyncHookQueue struct {
	mu    sync.Mutex
	items []asyncHookRewake
}

func (q *asyncHookQueue) push(item asyncHookRewake) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, item)
}

func (q *asyncHookQueue) pop() (asyncHookRewake, bool) {
	if q == nil {
		return asyncHookRewake{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return asyncHookRewake{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}
