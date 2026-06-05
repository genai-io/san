package hook

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/genai-io/san/v2/internal/setting"
)

type functionHookRegistration struct {
	Matcher string
	Hook    FunctionHook
}

type hookSource struct {
	Matcher string
	Hooks   []setting.HookCmd
	Source  string
}

type functionHookSource struct {
	Matcher string
	Hooks   []FunctionHook
	Source  string
}

type hookStore struct {
	mu            sync.RWMutex
	sessionHooks  map[EventType][]setting.Hook
	runtimeHooks  map[EventType][]setting.Hook
	sessionFuncs  map[EventType][]functionHookRegistration
	runtimeFuncs  map[EventType][]functionHookRegistration
	executedOnce  map[string]struct{}
	functionSeqNo atomic.Uint64
}

func newHookStore() *hookStore {
	return &hookStore{
		sessionHooks: make(map[EventType][]setting.Hook),
		runtimeHooks: make(map[EventType][]setting.Hook),
		sessionFuncs: make(map[EventType][]functionHookRegistration),
		runtimeFuncs: make(map[EventType][]functionHookRegistration),
		executedOnce: make(map[string]struct{}),
	}
}

func (s *hookStore) AddSessionHook(event EventType, matcher string, hook setting.HookCmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionHooks[event] = append(s.sessionHooks[event], setting.Hook{
		Matcher: matcher,
		Hooks:   []setting.HookCmd{hook},
	})
}

func (s *hookStore) AddRuntimeHook(event EventType, matcher string, hook setting.HookCmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeHooks[event] = append(s.runtimeHooks[event], setting.Hook{
		Matcher: matcher,
		Hooks:   []setting.HookCmd{hook},
	})
}

func (s *hookStore) AddSessionFunctionHook(event EventType, matcher string, hook FunctionHook) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	hook.ID = s.ensureFunctionHookIDLocked("session", event, hook.ID)
	s.sessionFuncs[event] = append(s.sessionFuncs[event], functionHookRegistration{
		Matcher: matcher,
		Hook:    hook,
	})
	return hook.ID
}

func (s *hookStore) AddRuntimeFunctionHook(event EventType, matcher string, hook FunctionHook) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	hook.ID = s.ensureFunctionHookIDLocked("runtime", event, hook.ID)
	s.runtimeFuncs[event] = append(s.runtimeFuncs[event], functionHookRegistration{
		Matcher: matcher,
		Hook:    hook,
	})
	return hook.ID
}

func (s *hookStore) RemoveSessionFunctionHook(event EventType, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return removeFunctionHookByID(s.sessionFuncs, event, id)
}

func (s *hookStore) RemoveRuntimeFunctionHook(event EventType, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return removeFunctionHookByID(s.runtimeFuncs, event, id)
}

func (s *hookStore) ClearSessionHooks() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionHooks = make(map[EventType][]setting.Hook)
	s.sessionFuncs = make(map[EventType][]functionHookRegistration)
	s.executedOnce = make(map[string]struct{})
}

func (s *hookStore) CollectHooks(event EventType, settings *setting.Data) []hookSource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var hooks []hookSource
	if settings != nil {
		for _, hook := range settings.Hooks[string(event)] {
			hooks = append(hooks, hookSource{
				Matcher: hook.Matcher,
				Hooks:   hook.Hooks,
				Source:  "settings",
			})
		}
	}
	for _, hook := range s.runtimeHooks[event] {
		hooks = append(hooks, hookSource{
			Matcher: hook.Matcher,
			Hooks:   hook.Hooks,
			Source:  "runtime",
		})
	}
	for _, hook := range s.sessionHooks[event] {
		hooks = append(hooks, hookSource{
			Matcher: hook.Matcher,
			Hooks:   hook.Hooks,
			Source:  "session",
		})
	}
	return hooks
}

func (s *hookStore) CollectFunctionHooks(event EventType) []functionHookSource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var hooks []functionHookSource
	for _, hook := range s.runtimeFuncs[event] {
		hookCopy := hook.Hook
		hooks = append(hooks, functionHookSource{
			Matcher: hook.Matcher,
			Hooks:   []FunctionHook{hookCopy},
			Source:  "runtime",
		})
	}
	for _, hook := range s.sessionFuncs[event] {
		hookCopy := hook.Hook
		hooks = append(hooks, functionHookSource{
			Matcher: hook.Matcher,
			Hooks:   []FunctionHook{hookCopy},
			Source:  "session",
		})
	}
	return hooks
}

func (s *hookStore) CheckOnce(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.executedOnce[key]; ok {
		return false
	}
	s.executedOnce[key] = struct{}{}
	return true
}

func (s *hookStore) HasHooks(event EventType, settings *setting.Data) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if settings != nil && len(settings.Hooks[string(event)]) > 0 {
		return true
	}
	return len(s.sessionHooks[event]) > 0 || len(s.runtimeHooks[event]) > 0 ||
		len(s.sessionFuncs[event]) > 0 || len(s.runtimeFuncs[event]) > 0
}

func (s *hookStore) ensureFunctionHookIDLocked(scope string, event EventType, current string) string {
	if current != "" {
		return current
	}
	seq := s.functionSeqNo.Add(1)
	return fmt.Sprintf("%s-%s-%d", scope, event, seq)
}

func removeFunctionHookByID(store map[EventType][]functionHookRegistration, event EventType, id string) bool {
	hooks := store[event]
	if len(hooks) == 0 {
		return false
	}

	filtered := hooks[:0]
	removed := false
	for _, hook := range hooks {
		if !removed && hook.Hook.ID == id {
			removed = true
			continue
		}
		filtered = append(filtered, hook)
	}

	if !removed {
		return false
	}
	if len(filtered) == 0 {
		delete(store, event)
		return true
	}
	store[event] = append([]functionHookRegistration(nil), filtered...)
	return true
}
