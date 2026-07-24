// Package plugin loads, installs, and enables plugin bundles that
// contribute skills, subagent definitions, slash commands, MCP servers,
// hooks, and env vars to the host application.
//
// The package exposes *Registry directly. Plugin's many cross-domain
// outputs (agent paths, skill paths, command paths, MCP servers, hooks,
// env vars) are consumed via the package-level free functions in
// integration.go — each downstream consumer (skill / subagent / command
// / mcp / setting) pulls what it needs. There is no shared narrow
// surface for a producer-side role interface.
package plugin

import "context"

// Options holds all dependencies for initialization.
type Options struct {
	CWD string
}

// Initialize loads plugins into the package-level *Registry.
func Initialize(ctx context.Context, opts Options) error {
	return defaultRegistry.Load(ctx, opts.CWD)
}

// Default returns the package-level *Registry. The registry is
// initialized at package load (empty) and populated by Initialize().
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
