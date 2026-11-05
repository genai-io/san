# Writing a Plugin

A plugin is a single directory that bundles any combination of skills,
subagents, slash commands, MCP servers, hooks, and env vars. Plugins are
the distribution unit: one git repo, one tarball, one `pluginspec.json`.

For the system-level design see [`packages/plugin.md`](../packages/plugin.md)
and [`concepts/extension-model.md`](../concepts/extension-model.md).

## Directory Layout

```
my-plugin/
├── pluginspec.json          # required: manifest
├── skills/
│   └── <name>/SKILL.md      # any number of skills
├── agents/
│   └── <name>.md            # any number of subagents
├── commands/
│   └── <name>.md            # any number of slash commands
├── mcp/
│   └── servers.json         # MCP server definitions
├── hooks/
│   └── hooks.json           # hook definitions (Claude-Code-compatible)
└── env/
    └── .env                 # env vars to merge when plugin is enabled
```

Everything except `pluginspec.json` is optional — a plugin can contribute
as little as one skill.

## Manifest (`pluginspec.json`)

```json
{
  "name": "github-flow",
  "version": "0.3.1",
  "description": "Issue triage, PR review, and release helpers",
  "author": "you@example.com",
  "homepage": "https://github.com/you/github-flow"
}
```

The schema is intentionally small. `name` must be unique across all
installed plugins; the directory name on disk does not have to match.

## Where Plugins Live

| Scope | Path |
|---|---|
| User | `~/.san/plugins/<name>/` |
| Project | `<project>/.san/plugins/<name>/` |
| Claude-compat | `~/.claude/plugins/<name>/`, `<project>/.claude/plugins/<name>/` |

Project plugins override user plugins by `name`.

## Installing

Three ways:

```bash
# From a local directory
san plugin install ./my-plugin

# From a git URL
san plugin install https://github.com/you/github-flow

# From the marketplace (if configured)
san plugin install github-flow
```

`san plugin install` runs the installer in `internal/plugin/installer.go`,
which copies the directory under the chosen scope and runs validation.

Inside the TUI, `/plugin` opens the plugin manager — install, enable,
disable, uninstall, switch scope, browse the marketplace.

## Enable State

Plugins are enabled per scope. Disabling a plugin removes its
contributions (skills / agents / commands / MCP / hooks) without
deleting files. Re-enable to restore.

State is persisted in `~/.san/plugins.json` and
`<project>/.san/plugins.json`.

## Contributions Push, Not Pull

When a plugin is enabled, `internal/plugin` *pushes* each contribution
into the relevant feature package:

| Contribution | Consumer |
|---|---|
| `skills/*/SKILL.md` | `internal/skill` (via `AddPluginSkills`) |
| `agents/*.md` | `internal/subagent` (via `AddPluginAgentPath`) |
| `commands/*.md` | `internal/command` (via `PluginCommandPaths` callback) |
| `mcp/servers.json` | `internal/mcp` (merged into `mcp.json`) |
| `hooks/hooks.json` | `internal/setting` (merged into `settings.json`'s `hooks`) |
| `env/.env` | `internal/setting` (merged into runtime env) |

This means the consumer packages do not import `plugin`; they receive
contributions as data.

## Minimal Working Plugin

```
my-plugin/
├── pluginspec.json
└── skills/
    └── say-hello/
        └── SKILL.md
```

`pluginspec.json`:

```json
{
  "name": "demo",
  "version": "0.1.0",
  "description": "Demo plugin",
  "author": "me"
}
```

`skills/say-hello/SKILL.md`:

```markdown
---
name: say-hello
description: Greet the user in their preferred style
---

Greet the user warmly in their preferred style.
```

Install + use:

```bash
san plugin install ./my-plugin
# in the TUI:
/plugin              # confirm "demo" is enabled
/say-hello           # invoke the skill
```

## Marketplace (optional)

A marketplace is a JSON file (locally or hosted) describing plugins:

```json
{
  "plugins": [
    {
      "name": "github-flow",
      "url": "https://github.com/you/github-flow",
      "version": "0.3.1",
      "description": "..."
    }
  ]
}
```

Configure marketplace URLs in `~/.san/settings.json`:

```json
{
  "marketplaces": ["https://example.com/plugins.json"]
}
```

`san plugin search` and `san plugin install <name>` then resolve through
the marketplace.

## Common Pitfalls

- **Forgot `pluginspec.json`.** Plugin is silently skipped at load time.
  Check `~/.san/logs/` for the warning.
- **Skill name collisions across plugins.** Disambiguate with `namespace:`
  in the SKILL.md frontmatter (e.g. `namespace: github`, then invoked as
  `/github:create-pr`).
- **Hooks shadow user hooks.** A plugin's `hooks/hooks.json` merges into
  the user's hooks at the same event/matcher key. If the user already
  configured the same event, the plugin's hook stacks; order is
  config-order.

## See Also

- [`packages/plugin.md`](../packages/plugin.md) — loader, installer,
  marketplace internals.
- [`writing-a-skill.md`](writing-a-skill.md), [`writing-a-subagent.md`](writing-a-subagent.md).
- [`concepts/extension-model.md`](../concepts/extension-model.md) — how
  the four primitives relate.
