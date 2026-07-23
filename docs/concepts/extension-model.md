# Extension Model

San is built so users can extend it without touching Go. There are **three
extension primitives** plus a **plugin source** that packages them. Each
primitive is a small markdown-or-process artifact placed in a known location.

| Primitive | What it is | Package | Where it lives |
|---|---|---|---|
| **Skill** | A markdown capability the model can discover and invoke. | [`skill`](../packages/2-feature/skill.md) | `~/.san/skills/<name>/SKILL.md` and project equivalents |
| **Slash Command** | A markdown file that injects a parameterized prompt. | [`command`](../packages/2-feature/command.md) | `~/.san/commands/<name>.md` and project equivalents |
| **Hook** | A shell command, HTTP endpoint, LLM call, or in-process callback fired at a named event. | [`hook`](../packages/2-feature/hook.md) | `settings.json` (`hooks` field) |

Tools are the inbound capability surface: built-ins live in [`tool`](../packages/2-feature/tool.md), and MCP contributes external tools through [`mcp`](../packages/2-feature/mcp.md).

Subagents are runtime workers, not an extension primitive. The `Agent` tool
launches one implicit general-purpose worker; San does not load selectable agent
definitions from user, project, persona, or plugin directories. See
[`subagent`](../packages/2-feature/subagent.md).

## Plugin is a Source, Not a Primitive

A **plugin** bundles any combination of skills, commands, MCP servers, hooks,
and environment variables. Skills and commands can also live standalone under
`~/.san/*` or `<project>/.san/*`.

```text
       ┌──────────────────────────────────────┐
       │               Plugin                 │
       │  skills · commands · MCP · hooks     │
       └───────────┬──────────┬───────────────┘
                   ▼          ▼
                 skill     command       + MCP, hooks, env
                   ▲          ▲
       ┌───────────┴──────────┴───────────────┐
       │ ~/.san/<surface>/ + project scope    │
       └──────────────────────────────────────┘
```

[`plugin`](../packages/2-feature/plugin.md) discovers plugins and pushes each
contribution to its consuming package. Consumers do not import plugin state
through a shared extension registry.

## Discovery Order

Markdown extension primitives resolve this precedence chain:

```text
project (.san/<surface>/)
    overrides
project plugins (.san/plugins/*/...)
    overrides
user (~/.san/<surface>/)
    overrides
user plugins (~/.san/plugins/*/...)
    overrides
Claude-compatible sources where supported
```

Higher-priority entries shadow lower ones by name (or by `name:path` for skill
namespaces). Enable state is persisted per scope.

## Frontmatter Convention

Markdown-defined skills and commands use YAML frontmatter followed by prompt or
instruction content. Exact fields vary by primitive; see the package docs for
[`skill`](../packages/2-feature/skill.md) and
[`command`](../packages/2-feature/command.md).

## See Also

- [`harness-channels`](harness-channels.md) — how skills and reminders reach the model.
- [`permission-model`](permission-model.md) — main and subagent permission modes.
- [`plugin`](../packages/2-feature/plugin.md) — install, marketplace, and enable mechanics.
