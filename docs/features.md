# Feature Overview

[简体中文](features.zh.md)

```text
San
|-- Memory and learning: Auto memory, project instructions, self-learning
|-- Collaboration: Subagents, agent messaging, session groups, Flow
|-- Sessions and automation: Sessions, tasks and processes, Autopilot, scheduling
|-- Tools and extensions: Built-in tools, web search, skills, plugins,
|                         MCP, hooks, slash commands
|-- Models and controls: Models and providers, personas, settings, permissions
`-- Interaction and inspection: CLI, Inspector
```

## Memory and learning

- [Auto memory](concepts/auto-memory.md) — The main agent stores durable Markdown notes. Background recall adds relevant notes to a tool result if ready, without blocking the turn. `User/project notes -> recall -> context`
- [Project instructions](concepts/harness-channels.md) — Repo conventions live alongside code. San loads user and project `AGENTS.md` files, rules, and local overrides in scope order, within a shared size limit. `Scoped files -> reminders`
- [Self-learning](packages/selflearn.md) — Off by default. After an eligible `Evolve` request, a background reviewer can manage agent-owned skills, but not edit user-authored ones. `Completed turn -> review -> reusable skill`

## Agent and session collaboration

- [Subagents](packages/subagent.md) — Delegate work to an agent with its own conversation and effective permissions. `Agent call -> worker -> foreground result or background task`
- [Agent messaging](packages/broker.md) — A process-local broker routes to the main agent or a worker by task ID. Child completions can wait in memory for their owner. `Message -> broker -> recipient`
- [Session groups](design/proposal-0003-session-groups.md) — `/group` connects independent sessions on the same machine. Disk-backed inboxes retain messages for offline members. `Sender -> member inbox on disk -> receiver`
- [Flow: collaboration blueprints](packages/flow.md) — `/flow run` schedules steps once their dependencies are met, using native tasks and checkpoints for retry or resume. `Blueprint -> scheduler -> tasks -> checkpoints`

## Sessions, tasks, and automation

- [Sessions](packages/session.md) — Committed events are the source of truth. Replay restores history and active context; compaction changes only the active context. `Event log -> validated replay -> history and context`
- [Tasks and processes](packages/task.md) — Shell jobs, workers, and schedulers are tracked separately from planning todos. Their owning session can inspect status and output or stop them. `Background work -> session-owned task -> inspect or stop`
- [Autopilot](guides/autopilot.md) — A side model weighs eligible approvals and decides whether to continue an active mission within its limit. `Turn ends -> mission check -> judge -> continue or wait`
- [Scheduling](packages/cron.md) — Cron jobs are checked inside the running San process, not by a daemon. Durable definitions survive restart. `Due prompt -> start if idle or queue if busy`

## Tools and extensions

- [Built-in tools](packages/tool.md) — File, shell, and other tools register by name and expose parameter schemas to the model. `Tool call -> permission check -> execute -> result`
- [Web search](packages/search.md) — `/search` chooses the backend; Exa is the default. Adapters normalize titles, links, and snippets for the model. `Query -> provider -> normalized results`
- [Skills](packages/skill.md) — San discovers reusable instructions in `SKILL.md`. Active skills expose their metadata; invocation loads the full instructions on demand. `SKILL.md -> registry -> instructions when invoked`
- [Plugins](packages/plugin.md) — Bundle skills, agents, commands, MCP servers, and hooks for installation together. `Plugin bundle -> enable -> load contributions`
- [MCP](packages/mcp.md) — San connects over stdio or HTTP to discover external tool schemas and route permitted calls back to the owning server. `Agent <-> MCP manager <-> server`
- [Hooks](packages/hook.md) — Match handlers to lifecycle events. Synchronous outcomes can stop an operation; async hooks run separately. `Event -> matching hook -> apply outcome or run separately`
- [Commands](packages/command.md) — One slash palette combines built-in, dynamic, and custom Markdown commands. `Slash input -> lookup -> handler or expanded prompt`

## Models, behavior, and permissions

- [Models and providers](packages/llm.md) — A common inference interface keeps the agent independent of model vendors. `/models` manages connections and selection. `Provider and model choice -> inference -> response`
- [Personas](concepts/persona.md) — A persona bundles prompt parts, skills, and settings. Prompts and skills update for the next inference; tool and permission settings wait for agent rebuild. `Select persona -> update prompt and skills / rebuild for tool policy`
- [Settings](packages/setting.md) — San merges user, project, and local settings with field-specific precedence and filters untrusted project settings. `Settings files -> trust filter and merge -> effective settings`
- [Permissions](concepts/permission-model.md) — Ordered rules, safety checks, and mode defaults decide each tool call. A required prompt becomes a refusal if nobody can answer. `Tool request -> policy -> allow, deny, or ask`

## Interaction and inspection

- [CLI](guides/getting-started.md) — `san` opens an interactive terminal UI; `san -p` or piped input runs once and writes the answer to stdout. `Input -> interactive UI or one-shot output`
- [Inspector](packages/inspector.md) — `san inspector` serves a read-only browser UI on loopback. It replays committed session events and streams new batches over SSE. `Session store -> local read-only server -> browser`
