// Package command is the registry of slash commands (built-in, dynamic,
// custom, plugin-scoped). Exposes *Registry directly.
package command

// PluginCommandPath describes a custom command file provided by a plugin.
type PluginCommandPath struct {
	Path      string
	Namespace string
	IsProject bool // true for project-scope, false for user-scope
}

// Options holds all dependencies for initialization.
type Options struct {
	CWD                string
	DynamicProviders   []func() []Info
	PluginCommandPaths func() []PluginCommandPath // injected plugin callback
}

// Initialize creates and installs the package-level *Registry.
func Initialize(opts Options) {
	defaultRegistry = &Registry{
		cwd:                  opts.CWD,
		dynamicInfoProviders: opts.DynamicProviders,
		pluginCommandPaths:   opts.PluginCommandPaths,
	}
}

// Default returns the package-level *Registry.
func Default() *Registry {
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level *Registry. Intended for
// tests. A nil argument restores a fresh empty *Registry.
func SetDefaultRegistry(s *Registry) {
	if s == nil {
		defaultRegistry = &Registry{}
		return
	}
	defaultRegistry = s
}

var defaultRegistry = &Registry{}
