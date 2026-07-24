// Package subagent owns the registry of custom agent definitions (markdown
// files under ~/.san/agents/ and <project>/.san/agents/) plus the
// *Executor that spawns one of them as a background core.Agent for a
// single invocation.
//
// The package exposes the concrete *Registry directly — no Service
// interface. The four production caller sites use four different
// subsets of *Registry's surface (Get from cmd; ListConfigs from
// view; PromptSection from agent.go; the whole registry from the TUI
// selector via an adapter). No shared narrow surface → no role
// interface earns its keep. TEMPLATE Rule 3.
//
// Executor construction goes through the package-level NewExecutor
// free function (in executor.go), not through any method on *Registry.
package subagent

import (
	"fmt"
	"sync"
)

// Options holds all dependencies for initialization.
type Options struct {
	CWD              string
	PluginAgentPaths func() []PluginAgentPath
}

// Initialize loads custom agents from all sources and initializes state stores.
func Initialize(opts Options) error {
	registry := NewRegistry()
	loadCustomAgents(opts.CWD, registry, opts.PluginAgentPaths)
	if err := registry.InitStores(opts.CWD); err != nil {
		return fmt.Errorf("failed to initialize agent stores: %w", err)
	}
	setDefaultRegistry(registry)
	return nil
}

// Default returns the package-level *Registry. The registry is
// initialized to an empty state at package load and populated by
// Initialize().
func Default() *Registry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level registry. Intended for
// tests. A nil argument restores a fresh empty *Registry.
func SetDefaultRegistry(r *Registry) {
	if r == nil {
		r = NewRegistry()
	}
	setDefaultRegistry(r)
}

// ResetDefaultRegistry restores a fresh empty *Registry. Intended for
// tests.
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
