// Package subagent owns the registry of agent type definitions (markdown
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

import "fmt"

// Options holds all dependencies for initialization.
type Options struct {
	CWD              string
	PluginAgentPaths func() []PluginAgentPath
}

// Initialize loads custom agents from all sources and initializes state stores.
func Initialize(opts Options) error {
	ClearPluginAgentPaths()

	if opts.PluginAgentPaths != nil {
		for _, pp := range opts.PluginAgentPaths() {
			AddPluginAgentPath(pp.Path, pp.Namespace)
		}
	}

	LoadCustomAgents(opts.CWD)

	if err := defaultRegistry.InitStores(opts.CWD); err != nil {
		return fmt.Errorf("failed to initialize agent stores: %w", err)
	}
	return nil
}

// Default returns the package-level *Registry. The registry is
// initialized to an empty state at package load and populated by
// Initialize().
func Default() *Registry {
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level registry. Intended for
// tests. A nil argument restores a fresh empty *Registry.
func SetDefaultRegistry(r *Registry) {
	if r == nil {
		defaultRegistry = NewRegistry()
		return
	}
	defaultRegistry = r
}

// ResetDefaultRegistry restores a fresh empty *Registry. Intended for
// tests.
func ResetDefaultRegistry() {
	defaultRegistry = NewRegistry()
}
