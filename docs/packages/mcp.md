---
package: github.com/genai-io/san/internal/mcp
layer: feature
---

# mcp

Model Context Protocol (MCP) client — connects to external MCP servers,
lists their tools, and routes tool calls to the corresponding server.

## Purpose

MCP is how San talks to **external** tool providers (file system
servers, GitHub servers, custom user servers, …). Each configured MCP
server is a separate subprocess (`stdio`) or HTTP endpoint that publishes
a list of tools; this package maintains the connection, surfaces those
tools as `core.ToolSchema` to the agent loop, and dispatches calls
through `ServerSet.CallTool`.

Config lives at `<project>/.san/mcp.json` (San) or
`<project>/.claude/mcp_servers.json` (Claude-compat).

When launched at a worktree root, native project configuration comes from that
worktree's `.san/mcp.json`, not the main checkout's copy. Git-tracked configuration
is available when present in the checked-out commit; uncommitted/ignored files are
not automatically shared, and subsequent edits are independent. Missing project
configuration does not trigger a main-checkout fallback. User-level configuration
and the existing `mcp.local.json` compatibility precedence remain unchanged.
Workspace trust still gates project servers; loading configuration does not connect
or execute them. Session-owned MCP processes use the session's execution directory.

## Contract

`mcp.ServerSet` holds the configured servers by name, including disabled or
not-yet-connected servers. Each live connection belongs to one server in the
set. `List` shows the servers; `Connect` and `Disconnect` change their live
connections. `ToolSchemas` lists tools and `CallTool` routes a call to the
server that provides it.

`mcp.Default()` returns the package-level set. `mcp.NewSession(...)` creates
a separate set for a session. Tests can replace the default with `SetDefault`
or restore an empty set with `ResetDefault`.

## Lifecycle

- Construction: `Initialize(Options{CWD, PluginServers})` reads config,
  merges plugin-supplied servers, but does **not** connect.
- Per-connect: `ConnectAll(ctx)` connects enabled servers in order. On a connection
  failure it closes the owned clients and returns an error.
- Reload: `/mcp` slash command edits config and reconnects.
- Concurrency: the server set is mutex-protected; per-client message routing
  is goroutine-safe.

## Tests

```
internal/mcp/config_test.go               — config parse/merge.
internal/mcp/worktree_config_test.go      — checked-out config, isolated edits, trust and runtime cwd.
internal/mcp/server_set_connecting_test.go  — connect/disconnect cycles.
internal/mcp/hooks_test.go                — lifecycle hook emission.
```

## See Also

- Code: `internal/mcp/`
- Tools: [`packages/tool.md`](tool.md) (MCP tools register here via `core_adapter`)
- Plugins: [`packages/plugin.md`](plugin.md) (plugins can ship MCP servers)
- Reference: [`reference/configuration.md`](../reference/configuration.md)
- Concepts: [`concepts/extension-model.md`](../concepts/extension-model.md)
- Layer: `feature`
