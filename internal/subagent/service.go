// Package subagent owns custom Agent runtime configuration and execution.
package subagent

import "sync"

// Default returns the package-level registry.
func Default() *Registry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level registry. Intended for tests.
// A nil argument restores a fresh empty registry.
func SetDefaultRegistry(r *Registry) {
	if r == nil {
		r = NewRegistry()
	}
	setDefaultRegistry(r)
}

// ResetDefaultRegistry restores a fresh empty registry. Intended for tests.
func ResetDefaultRegistry() {
	setDefaultRegistry(NewRegistry())
}

var (
	registryMu      sync.RWMutex
	defaultRegistry = NewRegistry()
)

func setDefaultRegistry(registry *Registry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	defaultRegistry = registry
}
