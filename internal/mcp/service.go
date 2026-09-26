// Package mcp is the Model Context Protocol client. It manages a set of
// configured MCP servers and exposes the tools they advertise to the
// agent loop.
//
// Callers use the concrete *Registry. Config editing (used by `san mcp edit`)
// is exposed as the free functions PrepareServerEdit / ApplyServerEdit.
package mcp

// Options holds all dependencies for initialization.
type Options struct {
	CWD           string
	PluginServers func() []PluginServer
}

// Initialize creates the MCP registry and installs it as the package-level
// default. Idempotent: callers may invoke it more than once (e.g. after a
// cwd change or plugin reload) and downstream callers reading
// DefaultRegistry will see the latest instance.
func Initialize(opts Options) error {
	reg, err := NewRegistry(opts.CWD)
	if err != nil {
		return err
	}
	if opts.PluginServers != nil {
		reg.PluginServers = opts.PluginServers
		reg.configs = reg.mergePluginMCPConfigs(reg.configs)
	}
	// Carry live connections across the swap. Initialize runs on every cwd
	// change and plugin reload, and a user-level server is configured in both
	// the old project and the new one — tearing it down would drop every
	// mcp__* tool from the agent mid-session, with nothing to reconnect it
	// (AutoConnect only runs at startup).
	reg.adoptLiveClients(DefaultRegistry())
	setDefaultRegistry(reg)
	return nil
}

// DefaultRegistry returns the package-level MCP registry. Returns the
// empty pre-Initialize registry if Initialize has not run.
//
// This is the only seam. There is no separate Service interface — every
// consumer (subagent executor, TUI selector, agent tool wiring,
// cmd subcommands) depends on *Registry directly. Tool execution goes
// through AsCoreTools. Config editing uses the free functions
// PrepareServerEdit / ApplyServerEdit.
func DefaultRegistry() *Registry {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return defaultRegistry
}

func setDefaultRegistry(reg *Registry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	defaultRegistry = reg
}

// SetDefaultRegistry replaces the package-level registry. Intended for
// tests. A nil argument restores the empty pre-Initialize registry.
func SetDefaultRegistry(reg *Registry) {
	if reg == nil {
		reg = newEmptyRegistry()
	}
	setDefaultRegistry(reg)
}

// ResetDefaultRegistry restores the empty pre-Initialize registry.
// Intended for tests.
func ResetDefaultRegistry() {
	setDefaultRegistry(newEmptyRegistry())
}
