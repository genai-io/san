package hook

import (
	"slices"

	"github.com/genai-io/san/internal/setting"
)

// ForSession snapshots hook configuration for an independently owned session.
// Owner-bound recording and delivery callbacks must be installed by the caller.
func (e *Engine) ForSession(sessionID, cwd, transcriptPath string) *Engine {
	e.mu.RLock()
	var settings *setting.Data
	if e.settings != nil {
		settings = e.settings.Clone()
	}
	child := NewEngine(settings, sessionID, cwd, transcriptPath)
	child.permissionMode = e.permissionMode
	child.llmCompleter, child.hookModel = e.llmCompleter, e.hookModel
	child.envProvider = e.envProvider
	if e.httpClient != nil {
		client := *e.httpClient
		child.httpClient = &client
	}
	e.mu.RUnlock()

	e.store.mu.RLock()
	defer e.store.mu.RUnlock()
	// Reuse the native settings clone for nested command headers and env lists.
	for _, pair := range []struct {
		source map[EventType][]setting.Hook
		target map[EventType][]setting.Hook
	}{{e.store.sessionHooks, child.store.sessionHooks}, {e.store.runtimeHooks, child.store.runtimeHooks}} {
		data := &setting.Data{Hooks: make(map[string][]setting.Hook, len(pair.source))}
		for event, hooks := range pair.source {
			data.Hooks[string(event)] = hooks
		}
		for event, hooks := range data.Clone().Hooks {
			pair.target[EventType(event)] = hooks
		}
	}
	for event, hooks := range e.store.sessionFuncs {
		child.store.sessionFuncs[event] = slices.Clone(hooks)
	}
	for event, hooks := range e.store.runtimeFuncs {
		child.store.runtimeFuncs[event] = slices.Clone(hooks)
	}
	child.store.functionSeqNo.Store(e.store.functionSeqNo.Load())
	return child
}
