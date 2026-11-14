# Extension Model

San is built so users can extend it without touching Go. There are
**four extension primitives** plus a **plugin source** that packages them.
Each primitive is a small markdown-or-process artifact the user puts in a
known directory; San discovers it at startup and exposes it through a
runtime registry.

| Primitive | What it is | Package | Where it lives |
|---|---|---|---|
| **Skill** | A markdown file the model can be made aware of, or invoke via slash command. | [`skill`](../packages/skill.md) | `~/.san/skills/<name>/SKILL.md` and project equivalents |
| **Subagent** | A markdown-defined agent with its own system prompt and tool subset; spawned via the `Agent` tool. | [`subagent`](../packages/subagent.md) | `~/.san/agents/<name>.md` and project equivalents |
| **Slash Command** | A markdown file that injects a parameterized prompt; invoked from the input box. | [`command`](../packages/command.md) | `~/.san/commands/<name>.md` and project equivalents |
| **Hook** | A shell command, HTTP endpoint, LLM call, or in-process callback fired at a named event. | [`hook`](../packages/hook.md) | `settings.json` (`hooks` field) |

Plus the inbound side:

| Primitive | What it is | Package |
|---|---|---|
| **Tool** | A capability the agent calls. Built-in or contributed by MCP. | [`tool`](../packages/tool.md), [`mcp`](../packages/mcp.md) |

## Plugin is a Source, Not a Primitive

A **plugin** is a *bundle* of any combination of the above. It is a single
directory the user installs; the plugin contributes some skills, some
agents, some commands, some MCP servers, some hooks, and optionally env
vars. The four primitives can also live without a plugin — they exist
standalone under `~/.san/*` or `<project>/.san/*`.

So the right mental model:

```
       ┌────────────────────────────────────────┐
       │                Plugin                  │
       │   (one directory, version-pinnable)    │
       └──────┬────────┬───────┬──────┬─────────┘
              │        │       │      │
              ▼        ▼       ▼      ▼
          skill    agent   command   mcp    + hooks, env
              ▲        ▲       ▲      ▲
              │        │       │      │
       ┌──────┴────────┴───────┴──────┴─────────┐
       │     ~/.san/<surface>/  + project       │
       │     (loose files, no plugin needed)    │
       └────────────────────────────────────────┘
```

[`plugin`](../packages/plugin.md) discovers plugins and **pushes** the
per-primitive contributions to each consuming package at startup via
callbacks (see `Options.PluginSkillPaths`, `Options.PluginAgentPaths`,
etc.). The consumer doesn't import `plugin`.

## Discovery Order

Each primitive resolves the same precedence chain:

```
project (.san/<surface>/)
    overrides
project plugins (.san/plugins/*/...)
    overrides
user (~/.san/<surface>/)
    overrides
user plugins (~/.san/plugins/*/...)
    overrides
Claude-compat (~/.claude/<surface>/, .claude/<surface>/)
```

Higher-priority entries shadow lower ones by **name** (or by `name:path`
for skills with namespaces). The `IsEnabled` / state flags are persisted
per scope so the user can disable a plugin-contributed skill without
removing the plugin.

## Frontmatter Convention

All markdown-defined primitives share the same general shape:

```markdown
---
name: my-skill              # required
description: One-liner      # required for selectors
namespace: optional         # only skills support this
allowed-tools: [Read, Bash] # tool subset (subagent + skill)
argument-hint: <hint>       # for slash commands
---

The body of the file becomes the prompt / instruction content.
```

Exact frontmatter fields vary by primitive — see the per-package docs:
[`skill`](../packages/skill.md), [`subagent`](../packages/subagent.md),
[`command`](../packages/command.md).

## Reload Contract

The TUI reloads extensions at explicit lifecycle boundaries; it does not watch
all extension or settings files for changes.

| Trigger | Discovery and application |
|---|---|
| Startup / `--plugin-dir` | Load plugins before their consumers; build the first agent from the resulting catalogues. |
| `/reload-plugins` | Rescan native and Claude-compatible plugins, then reload their consumers. |
| Plugin picker install / uninstall | Reload consumers after a successful operation. Picker enable/disable and slash-command installation require `/reload-plugins`. |
| Working-directory change | Rediscover plugins and trusted project configuration, then reload consumers for the new cwd. |
| Settings / tool panels | Persist and reload the existing settings handle; this is not a full extension rescan. Agent toolset changes apply at the next turn. |
| Persona file change reported by a tool | Rescan personas and reapply bundled skills and agent restrictions; not a general file watcher. |
| MCP tools-changed notification | Refresh the connected server's tools, then reconcile on the UI loop. No filesystem discovery. |

A full consumer reload replaces settings, skill, command, persona and MCP
registries; subagent and plugin registries are updated in place. Selectors and
tool-policy callbacks are rebound, and command suggestions use the new catalogue.
Replacing a registry closes its old picker and discards cached rows; in-place
registries refresh cached rows when their pickers reopen. Composer,
history, queued messages and pending skill invocations remain intact; unchanged
plugin operations and the Settings popup's selected tab remain intact too.
Persona overlays, hooks and queued system reminders use the reloaded context.

The current turn is not cancelled. Captured agent dependencies, including the
permission policy, are rebuilt before the next turn against the same native
session owner. Queued successors and cancellation-held input cross this boundary
on a later UI update, after the terminal consumer acknowledgment; input injected
into an already admitted turn does not restart it. This is not a retroactive
policy change for admitted work.

MCP replacement adopts only connected clients with identical configurations,
rebinds their notifications and retires the outgoing manager. Removed or changed
clients close asynchronously; a late connection into a retired manager closes
instead of being published. New/changed servers are **not** automatically started
by reload: connect explicitly through `/mcp` or on application startup. Live MCP
tool wrappers are reconciled immediately, including removal of the last server;
background notifications coalesce and read only the current services on the UI
loop. Late connection results from an outgoing manager are ignored by the picker.

Reload is best effort, not an atomic transaction. Plugin operation failures do
not trigger consumer reload; loader failures are logged or skipped according to
the owning package. MCP initialization failure keeps the previous manager. A
failed tools-list refresh keeps its last successful list and does not notify.
There is no cross-registry rollback. ACP sessions load their own registries at
new/load and do not expose these TUI management commands.

Entrypoints: `internal/app/init.go`, `model_lifecycle.go`, `model_workspace.go`
and `mcp_tools.go`. Regressions: `extension_reload_test.go`,
`extension_reload_queue_test.go`, `mcp_tools_sync_test.go`,
`input/selector_deps_test.go` and `internal/mcp/adopt_clients_test.go`.

```sh
go test -race ./internal/app ./internal/app/input ./internal/mcp
```

## See Also

- [`concepts/harness-channels.md`](harness-channels.md) — how skill /
  reminder content reaches the model.
- [`concepts/permission-model.md`](permission-model.md) — how a
  subagent's `allowed-tools` interacts with the permission gate.
- [`packages/plugin.md`](../packages/plugin.md) — install / marketplace /
  enable mechanics.
