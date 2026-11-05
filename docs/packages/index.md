# Packages

Implementation notes for Go packages under `internal/` and the `cmd`
entrypoint live directly in `docs/packages/`. Browse the package links below;
see [dependency rules](../reference/dependency-rules.md) and the
[package map](../reference/package-map.md) for layer assignments and ownership.

Filenames match the package name with no suffix (`hook.md`, not `hooks.md`).
Translations sit beside the source with a `.zh.md` suffix (`hook.zh.md`).
Every page follows [`TEMPLATE.md`](TEMPLATE.md).

## Entrypoint

| Package | One-liner |
|---|---|
| [`cmd`](cmd.md) | Entrypoint: flag parsing, dependency wiring, provider blank-imports. |

## Application shell

| Package | One-liner |
|---|---|
| [`app`](app.md) | Bubble Tea TUI shell, MVU loop, sub-model decomposition. *Seed page; rewrite to TEMPLATE pending.* |

## Features

| Package | One-liner |
|---|---|
| [`agent`](agent.md) | Main agent session lifecycle (Start/Stop/Send/Outbox + permission bridge). |
| [`broker`](broker.md) | Routes messages between agents: addresses keyed by agent id, for main↔subagent messages and completions. |
| [`command`](command.md) | Slash command registry (builtin + dynamic + custom + plugin-scoped). |
| [`cron`](cron.md) | Cron expressions and one-shot scheduling for `/loop` and `/schedule`. |
| [`hook`](hook.md) | Pre/post hook engine with command / HTTP / LLM / function executors. |
| [`inspector`](inspector.md) | Local web UI for transcript replay; SSE live-tail. |
| [`llm`](llm.md) | Provider registry, model store, and the per-turn `*ai.Client` the agent loop streams through. |
| [`mcp`](mcp.md) | MCP client + transport + `Caller` for external tool servers. |
| [`plugin`](plugin.md) | Plugin loader / installer / marketplace; pushes contributions to other feature packages. |
| [`reminder`](reminder.md) | `<system-reminder>` queue with provider re-emission. |
| [`search`](search.md) | Pluggable web search backends behind a small `Provider` interface. |
| [`session`](session.md) | Durable identity, product state, native message writer, resume, and fork. |
| [`setting`](setting.md) | Settings loader + central permission decision gate. |
| [`selflearn`](selflearn.md) | Background review loop that creates and refines agent-created skills. |
| [`skill`](skill.md) | Skill loader, state store, active-skills block consumed by the `skills-directory` reminder. |
| [`subagent`](subagent.md) | Subagent registry + `Executor` that spawns background `core.Agent` instances. |
| [`task`](task.md) | Background task manager (bash and agent tasks). |
| [`tool`](tool.md) | Tool registry, schemas, permission gate, side-effect store. |
| [`transcript`](transcript.md) | Native event log, ancestry, recovery, writer leases, and derived summaries. |
| [`workflow`](workflow.md) | Markdown-defined graph of subagent turns: parser, validation, scheduler, summary. |

## Shared contracts

| Package | One-liner |
|---|---|
| [`core`](core.md) | Agent primitive, `System`, `Tools`, `LLM`, `Message` — the stable contracts every feature shares. |

## Infrastructure

| Package | One-liner |
|---|---|
| [`infrastructure`](infrastructure.md) | `log` / `secret` / `filecache` / `markdown` / `confdir` / `proc` — stateless helpers documented together. |

## Reference-Shape Pages

Model citizens for what `feature` packages should look like — minimal interface,
concrete return types, no kitchen-sink `Service`:

- [`reminder`](reminder.md) — concrete `*Reminder` struct with closure-backed providers.
- [`search`](search.md) — pure consumer-defined `Provider`, no singleton.
