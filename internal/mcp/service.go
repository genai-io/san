// Package mcp is the Model Context Protocol client. It manages a set of
// configured MCP servers and exposes the tools they advertise to the
// agent loop.
//
// Two interfaces describe what the package can be asked to do:
//
//   - Tools   — list MCP tool schemas, call one by name.
//   - Servers — list configured MCP servers, connect/disconnect, look up config.
//
// Config editing (used by `san mcp edit`) is exposed as the two free
// functions PrepareServerEdit / ApplyServerEdit. Full server-state
// mutation (RemoveServer / SetDisabled / SetConnectError / …) is
// available on the concrete *Registry — only the TUI /mcp selector
// needs that surface today, and a 10-method interface would just be
// *Registry rewritten.
//
// *Registry implements both interfaces. Callers narrow by declaration:
//
//	var tools mcp.Tools = mcp.DefaultRegistry()
package mcp

import (
	"context"

	"github.com/genai-io/san/internal/core"
)

// Tools lets a caller list and execute MCP tools across all connected
// servers. List with GetToolSchemas, invoke with CallTool. Implemented
// by *Registry.
//
// Consumers: agent main loop, slash-command tool selector, subagent
// executor. Each takes Tools rather than *Registry to express that it
// only listens to tool listing and tool execution, not server
// management.
type Tools interface {
	GetToolSchemas() []core.ToolSchema
	CallTool(ctx context.Context, fullName string, args map[string]any) (*ToolResult, error)
}

// Servers lets a caller manage MCP server connections: enumerate
// configured servers, connect or disconnect them individually or in
// bulk, and read per-server config. Implemented by *Registry.
//
// Consumers: the ConnectServers free function (which the subagent
// executor uses to bring up a curated server set per invocation). The
// TUI /mcp selector needs methods beyond this interface (RemoveServer,
// SetDisabled, SetConnectError, …) and depends on *Registry directly.
type Servers interface {
	List() []Server
	Connect(ctx context.Context, name string) error
	Disconnect(name string)
	ConnectAll(ctx context.Context) []error
	DisconnectAll()
	GetConfig(name string) (ServerConfig, bool)
}

// Compile-time guarantee that *Registry satisfies both interfaces.
// Adding a method to *Registry never breaks consumers; removing a
// method from an interface requires updating both sides.
var (
	_ Tools   = (*Registry)(nil)
	_ Servers = (*Registry)(nil)
)

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
	defaultRegistry = reg
	return nil
}

// DefaultRegistry returns the package-level MCP registry. Returns the
// empty pre-Initialize registry if Initialize has not run.
//
// This is the only seam. There is no separate Service interface — every
// consumer (subagent executor, TUI selector, agent tool wiring,
// cmd subcommands) depends on *Registry directly. Tool execution goes
// through NewCaller(reg). Config editing uses the free functions
// PrepareServerEdit / ApplyServerEdit.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// SetDefaultRegistry replaces the package-level registry. Intended for
// tests. A nil argument restores the empty pre-Initialize registry.
func SetDefaultRegistry(reg *Registry) {
	if reg == nil {
		defaultRegistry = newEmptyRegistry()
		return
	}
	defaultRegistry = reg
}

// ResetDefaultRegistry restores the empty pre-Initialize registry.
// Intended for tests.
func ResetDefaultRegistry() {
	defaultRegistry = newEmptyRegistry()
}
