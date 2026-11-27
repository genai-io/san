// Package mcp is the Model Context Protocol client. It manages a set of
// configured MCP servers and exposes the tools they advertise to the
// agent loop.
//
// Callers use the concrete *ServerSet. Config editing (used by `san mcp edit`)
// is exposed as the free functions PrepareServerEdit / ApplyServerEdit.
package mcp

// Options holds all dependencies for initialization.
type Options struct {
	CWD           string
	PluginServers func() []PluginServer
}

// Initialize creates the MCP server set and installs it as the package-level
// default. Idempotent: callers may invoke it more than once (e.g. after a
// cwd change or plugin reload) and downstream callers reading
// Default will see the latest instance.
func Initialize(opts Options) error {
	reg, err := NewServerSet(opts.CWD)
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
	reg.adoptLiveClients(Default())
	setDefault(reg)
	return nil
}

// Default returns the package-level MCP server set. Returns the
// empty pre-Initialize server set if Initialize has not run.
//
// This is the only seam. There is no separate Service interface — every
// consumer (subagent executor, TUI selector, agent tool wiring,
// cmd subcommands) depends on *ServerSet directly. Tool execution goes
// through AsCoreTools. Config editing uses the free functions
// PrepareServerEdit / ApplyServerEdit.
func Default() *ServerSet {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultServers
}

func setDefault(reg *ServerSet) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultServers = reg
}

// SetDefault replaces the package-level server set. Intended for
// tests. A nil argument restores the empty pre-Initialize server set.
func SetDefault(reg *ServerSet) {
	if reg == nil {
		reg = newEmptyServerSet()
	}
	setDefault(reg)
}

// ResetDefault restores the empty pre-Initialize server set.
// Intended for tests.
func ResetDefault() {
	setDefault(newEmptyServerSet())
}
