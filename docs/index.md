# Documentation

## Get started

- [Getting started](guides/getting-started.md) — installation, first run, and everyday usage.
- [Configuration](reference/configuration.md) — models, credentials, shell, and config files.
- [Commands](reference/slash-commands.md) — interactive command reference.
- [Permissions](concepts/permission-model.md) — approvals and execution modes.

## Features

- [Feature overview](features.md) — capabilities grouped by module, with links to details.
- [Auto memory](concepts/auto-memory.md) — persistent memory and recall.
- [Multi-agent collaboration](features.md#agent-and-session-collaboration) — subagents, messaging, session groups, and workflows.
- [Autopilot](guides/autopilot.md) — goal-driven execution.
- [Inspector](guides/inspector.md) — inspect and replay runs.

## Customize and extend

- [System prompts](concepts/harness-channels.md) — shape agent instructions and context.
- [Personas](concepts/persona.md) — bundle prompts, skills, agents, and settings.
- [Skills](guides/writing-a-skill.md) — write reusable task procedures.
- [Subagents](guides/writing-a-subagent.md) — define specialized agents.
- [Plugins](guides/writing-a-plugin.md) — package extensions.
- [MCP](packages/2-feature/mcp.md) · [Hooks](packages/2-feature/hook.md) · [Providers](guides/writing-a-provider.md) — integrate tools, automation, and models.

## Develop and contribute

- [Development](operations/development.md) — build, test, and contribute.
- [Architecture](concepts/architecture.md) — overview diagram and runtime design.
- [Package map](reference/package-map.md) · [Package details](packages/index.md) — code ownership and implementation.
- [Dependency rules](reference/dependency-rules.md) · [Design principles](design/principles.md) — engineering conventions.
- [Design history](design/index.md) — proposals and decisions.
- [Benchmarks](operations/benchmark.md) — performance measurements.

## Full indexes

[Guides](guides/index.md) · [Concepts](concepts/index.md) · [Reference](reference/index.md) · [Operations](operations/index.md) · [Packages](packages/index.md)

## Maintaining these docs

Keep README focused on the product and quick start. Update feature and package
docs alongside code changes; update the package map and dependency rules when
ownership or layers change. Use concepts for cross-cutting explanations, proposals
for designs, and decision records for durable architectural choices.

Work plans belong in `notes/active/` and `notes/completed/`, not in the user-facing
documentation. Known follow-ups live in `notes/tech-debt.md`.
