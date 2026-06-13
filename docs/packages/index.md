# Packages

One document per Go package under `internal/` that has non-trivial behavior.
Filename matches the package name with no suffix (`hook.md`, not
`hook-engine.md` or `hooks.md`). Subpackages with their own design surface
get a directory: `packages/<pkg>/<sub>.md`.

Every page in this directory must follow [`TEMPLATE.md`](TEMPLATE.md). The
template is enforced by review and (later) a doc linter.

## Layer Index

Use [`../reference/package-map.md`](../reference/package-map.md) for the
authoritative layer assignment of each package. The pages here explain how
each package works internally and what contract it exposes upward.

## Pages

| Package | Layer | One-liner |
|---|---|---|
| [`agent`](agent.md) | feature | Main agent session lifecycle (Start/Stop/Send/Outbox + permission bridge). |
| [`command`](command.md) | feature | Slash command registry (builtin + dynamic + custom + plugin-scoped). |
| [`core`](core.md) | core | Agent primitive, `System`, `Tools`, `LLM`, `Message` — the stable contracts every feature shares. |
| [`cron`](cron.md) | feature | Cron expressions and one-shot scheduling for `/loop` and `/schedule`. |
| [`hook`](hook.md) | feature | Pre/post hook engine with command / HTTP / LLM / function executors. |
| [`inspector`](inspector.md) | feature | Local web UI for transcript replay; SSE live-tail. |
| [`llm`](llm.md) | feature | Provider registry, model store, `Client` factory implementing `core.LLM`. |
| [`mcp`](mcp.md) | feature | MCP client + transport + `Caller` for external tool servers. |
| [`plugin`](plugin.md) | feature | Plugin loader / installer / marketplace; pushes contributions to other feature packages. |
| [`infrastructure`](infrastructure.md) | infrastructure | `log` / `secret` / `filecache` / `markdown` — stateless helpers documented together. |
| [`reminder`](reminder.md) | feature | `<system-reminder>` queue with provider re-emission; reference shape for what packages should look like post-refactor. |
| [`search`](search.md) | feature | Pluggable web search backends behind a small `Provider` interface. |
| [`session`](session.md) | feature | Transcript persistence, resume, fork, projection. |
| [`setting`](setting.md) | feature | Settings loader + central permission decision gate. |
| [`skill`](skill.md) | feature | Skill loader, state store, active-skills block consumed by the `skills-directory` reminder. |
| [`subagent`](subagent.md) | feature | Subagent registry + `Executor` that spawns background `core.Agent` instances. |
| [`task`](task.md) | feature | Background task manager (bash and agent tasks). |
| [`tool`](tool.md) | feature | Tool registry, schemas, permission gate, side-effect store. |
| [`ui`](ui.md) | app | Bubble Tea TUI shell, MVU loop, sub-model decomposition. *Seed page; rewrite to TEMPLATE pending.* |
| [`worktree`](worktree.md) | feature | Thin wrapper over `git worktree add/remove` for subagent isolation. |

## Reference-Shape Pages

Two packages here are model citizens for what `feature` packages should
look like after the PR-3 refactor — minimal interface, concrete return
types, no kitchen-sink `Service`:

- [`reminder.md`](reminder.md) — concrete `*Service` struct, small 2-method `Provider` interface.
- [`search.md`](search.md) — pure consumer-defined `Provider`, no singleton.
- [`worktree.md`](worktree.md) — two functions, no types.

