# Extension Model

San is built so users can extend it without touching Go. There are
**four extension primitives** plus a **plugin source** that packages them.
Each primitive is a small markdown-or-process artifact the user puts in a
known directory; San discovers it at startup and exposes it through a
runtime registry.

| Primitive | What it is | Package | Where it lives |
|---|---|---|---|
| **Skill** | A markdown file the model can be made aware of, or invoke via slash command. | [`skill`](../packages/2-feature/skill.md) | `~/.san/skills/<name>/SKILL.md` and project equivalents |
| **Subagent** | A markdown-defined agent type with its own system prompt and tool subset; spawned via the `Agent` tool. | [`subagent`](../packages/2-feature/subagent.md) | `~/.san/agents/<name>.md` and project equivalents |
| **Slash Command** | A markdown file that injects a parameterized prompt; invoked from the input box. | [`command`](../packages/2-feature/command.md) | `~/.san/commands/<name>.md` and project equivalents |
| **Hook** | A shell command, HTTP endpoint, LLM call, or in-process callback fired at a named event. | [`hook`](../packages/2-feature/hook.md) | `settings.json` (`hooks` field) |

Plus the inbound side:

| Primitive | What it is | Package |
|---|---|---|
| **Tool** | A capability the agent calls. Built-in or contributed by MCP. | [`tool`](../packages/2-feature/tool.md), [`mcp`](../packages/2-feature/mcp.md) |

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

[`plugin`](../packages/2-feature/plugin.md) discovers plugins and **pushes** the
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
[`skill`](../packages/2-feature/skill.md), [`subagent`](../packages/2-feature/subagent.md),
[`command`](../packages/2-feature/command.md).

## See Also

- [`concepts/harness-channels.md`](harness-channels.md) — how skill /
  reminder content reaches the model.
- [`concepts/permission-model.md`](permission-model.md) — how a
  subagent's `allowed-tools` interacts with the permission gate.
- [`packages/plugin.md`](../packages/2-feature/plugin.md) — install / marketplace /
  enable mechanics.
