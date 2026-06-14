---
package: github.com/genai-io/san/internal/mcp
layer: feature
---

# mcp

Model Context Protocol (MCP) client — connects to external MCP servers,
lists their tools, and exposes a `Caller` that the tool registry uses to
invoke them. Transport implementations live in `internal/mcp/transport/`.

## Purpose

MCP is how San talks to **external** tool providers (file system
servers, GitHub servers, custom user servers, …). Each configured MCP
server is a separate subprocess (`stdio`) or HTTP endpoint that publishes
a list of tools; this package maintains the connection, surfaces those
tools as `core.ToolSchema` to the agent loop, and dispatches calls
through `Caller`.

Config lives at `<project>/.san/mcp.json` (San) or
`<project>/.claude/mcp_servers.json` (Claude-compat).

## Contract

The package has four natural roles. Two surface as small interfaces;
one uses free functions; one stays on the concrete `*Registry`.

| Role | Shape | Consumers |
|---|---|---|
| **Tools** — tool discovery + execution | `interface{ GetToolSchemas; CallTool }` | agent main loop, slash-command tool selector, subagent executor |
| **Servers** — server listing + connect lifecycle | `interface{ List; Connect; Disconnect; ConnectAll; DisconnectAll; GetConfig }` | `mcp.ConnectServers` (free function), subagent executor |
| **ConfigStore** — load / edit / save server definitions | free functions `PrepareServerEdit` / `ApplyServerEdit` | `san mcp edit` CLI subcommand |
| **Manager** — full server-state mutation (add / remove / set-disabled / set-status) | concrete `*Registry` | TUI `/mcp` selector — needs the wide surface and there is exactly one consumer |

`*Registry` satisfies both interfaces; compile-time checks guarantee
this. Consumers narrow by declaration:

```go
var tools   mcp.Tools   = mcp.DefaultRegistry()
var servers mcp.Servers = mcp.DefaultRegistry()
```

```go
package mcp

// Tools — list MCP tool schemas and call one by name. Implemented by *Registry.
type Tools interface {
    GetToolSchemas() []core.ToolSchema
    CallTool(ctx context.Context, fullName string, args map[string]any) (*ToolResult, error)
}

// Servers — manage MCP server connections. Implemented by *Registry.
type Servers interface {
    List() []Server
    Connect(ctx context.Context, name string) error
    Disconnect(name string) error
    ConnectAll(ctx context.Context) []error
    DisconnectAll()
    GetConfig(name string) (ServerConfig, bool)
}

// *Registry is the only implementation; covers the Manager role and
// satisfies both interfaces above.
type Registry struct { /* internal fields */ }

var (
    _ Tools   = (*Registry)(nil)
    _ Servers = (*Registry)(nil)
)

// Free functions.
func NewCaller(tools Tools) *Caller
func PrepareServerEdit(reg *Registry, name string) (*EditInfo, error)
func ApplyServerEdit(reg *Registry, info *EditInfo) error
func ConnectServers(ctx context.Context, servers Servers, serverNames []string) (cleanup func(), errs []error)

// Package-level access.
func Initialize(opts Options) error
func DefaultRegistry() *Registry
func SetDefaultRegistry(reg *Registry)   // test-only
func ResetDefaultRegistry()              // test-only
```

### Why two role interfaces, not one god union

- Consumers that just list/call tools (agent loop, slash-command tool
  selector, subagent) depend on `Tools` — two methods.
- Consumers that browse and connect servers (`ConnectServers`,
  subagent executor) depend on `Servers` — six methods.
- The TUI `/mcp` selector mutates server state (`RemoveServer`,
  `SetDisabled`, `SetConnectError`, …) and takes `*Registry` directly.
  A 10-method interface here would just be `*Registry` renamed —
  TEMPLATE Rule 1 says no.

`*Registry` is the implementation; role interfaces are the public face
that callers narrow to.

## Internals

- `Registry` (`registry.go`) — server name → `Client` map; merges
  user/project/plugin config.
- `Client` (`client.go`) — one per server; owns the transport
  connection, request/response framing, tool list cache.
- `Caller` (`caller.go`) — invokes a tool on the right server based on
  tool name (MCP tool names are namespaced).
- `transport/` — stdio and HTTP transports implementing the MCP
  protocol.
- `config.go` + `edit.go` — load/edit the JSON config files.
- `core_adapter.go` — wraps MCP `tool/call` into `core.Tool` interface.
- `hooks.go` — fires lifecycle hooks (server connect/disconnect).

## Lifecycle

- Construction: `Initialize(Options{CWD, PluginServers})` reads config,
  merges plugin-supplied servers, but does **not** connect.
- Per-connect: `ConnectAll(ctx)` opens transports in parallel; failures
  surface in the returned `[]error`.
- Reload: `/mcp` slash command edits config and reconnects.
- Concurrency: registry is mutex-protected; per-client message routing
  is goroutine-safe.

## Tests

```
internal/mcp/config_test.go               — config parse/merge.
internal/mcp/registry_connecting_test.go  — connect/disconnect cycles.
internal/mcp/hooks_test.go                — lifecycle hook emission.
```

## See Also

- Code: `internal/mcp/`, `internal/mcp/transport/`
- Tools: [`packages/tool.md`](tool.md) (MCP tools register here via `core_adapter`)
- Plugins: [`packages/plugin.md`](plugin.md) (plugins can ship MCP servers)
- Reference: [`reference/configuration.md`](../../reference/configuration.md)
- Concepts: [`concepts/extension-model.md`](../../concepts/extension-model.md)
- Layer: `feature`
