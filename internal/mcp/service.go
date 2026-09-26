// Package mcp is the Model Context Protocol client. It manages a set of
// configured MCP servers and exposes the tools they advertise to the
// agent loop.
//
// Callers use the concrete *Manager. Config editing (used by `san mcp edit`)
// is exposed as the free functions PrepareServerEdit / ApplyServerEdit.
package mcp

// Options holds all dependencies for initialization.
type Options struct {
	CWD           string
	PluginServers func() []PluginServer
}

// Initialize creates the MCP manager and installs it as the package-level
// default. Idempotent: callers may invoke it more than once (e.g. after a
// cwd change or plugin reload) and downstream callers reading
// DefaultManager will see the latest instance.
func Initialize(opts Options) error {
	reg, err := NewManager(opts.CWD)
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
	reg.adoptLiveClients(DefaultManager())
	setDefaultManager(reg)
	return nil
}

// DefaultManager returns the package-level MCP manager. Returns the
// empty pre-Initialize manager if Initialize has not run.
//
// This is the only seam. There is no separate Service interface — every
// consumer (subagent executor, TUI selector, agent tool wiring,
// cmd subcommands) depends on *Manager directly. Tool execution goes
// through AsCoreTools. Config editing uses the free functions
// PrepareServerEdit / ApplyServerEdit.
func DefaultManager() *Manager {
	managerMu.RLock()
	defer managerMu.RUnlock()
	return defaultManager
}

func setDefaultManager(reg *Manager) {
	managerMu.Lock()
	defer managerMu.Unlock()
	defaultManager = reg
}

// SetDefaultManager replaces the package-level manager. Intended for
// tests. A nil argument restores the empty pre-Initialize manager.
func SetDefaultManager(reg *Manager) {
	if reg == nil {
		reg = newEmptyManager()
	}
	setDefaultManager(reg)
}

// ResetDefaultManager restores the empty pre-Initialize manager.
// Intended for tests.
func ResetDefaultManager() {
	setDefaultManager(newEmptyManager())
}
