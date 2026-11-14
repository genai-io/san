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

TUI reload is explicit; there is no general file watcher.

| Trigger | Effect |
|---|---|
| `/reload-plugins` | Rescan plugins and reload their consumers. |
| Plugin picker install / uninstall | Reload on success. Enable/disable and slash-command installs need `/reload-plugins`. |
| Cwd change | Reload extensions and trusted project configuration. |
| Settings / tool panels | Update settings without rescanning extensions. |
| Persona edits reported by tools | Rescan personas and reapply their skills and agent restrictions. |
| MCP tools-changed | Refresh tools and reconcile on the UI loop. |

Reload replaces settings, skills, commands, personas and MCP; plugin and subagent
registries update in place. Replaced pickers close; in-place pickers refresh on
reopening. Input, queues, session ownership and unchanged plugin operations survive.

The current turn continues. Captured dependencies rebuild before the next turn,
including queued input, after the terminal receipt is acknowledged.

MCP tools reconcile immediately. Unchanged connections are reused; notifications
are rebound and the old manager retires. Removed, changed or late connections close.
New/changed servers need an explicit connection or application restart;
stale connection results are ignored.

Reload is best effort, with no cross-registry rollback. MCP initialization or
tools-refresh failure keeps the previous manager or tool list respectively.
ACP loads session-local registries at new/load; TUI reload commands do not apply.

## See Also

- [`concepts/harness-channels.md`](harness-channels.md) — how skill /
  reminder content reaches the model.
- [`concepts/permission-model.md`](permission-model.md) — how a
  subagent's `allowed-tools` interacts with the permission gate.
- [`packages/plugin.md`](../packages/plugin.md) — install / marketplace /
  enable mechanics.
