# Feature Overview

[简体中文](features.zh.md)

**Design note:** [Minimal overhead (Chinese)](concepts/minimal-overhead.zh.md) explains San’s runtime footprint, lean system prompt, and prompt cache reuse.

```text
+-----+
| San |
+--+--+
   |
   +-- [ Memory and learning ]
   |   Auto memory / Project instructions / Self-learning
   |
   +-- [ Agent and session collaboration ]
   |   Subagents / Agent messaging / Session groups / Flow
   |
   +-- [ Sessions, tasks, and automation ]
   |   Sessions / Tasks and processes / Autopilot / Scheduling
   |
   +-- [ Tools and extensions ]
   |   Built-in tools / Web search / Skills / Plugins
   |   MCP / Hooks / Slash commands
   |
   +-- [ Models, behavior, and permissions ]
   |   Models and providers / Personas / Settings / Permissions
   |
   +-- [ Interaction and inspection ]
       CLI / ACP / Inspector
```

## Memory and learning

- [Auto memory](concepts/auto-memory.md) — Carries durable knowledge between sessions without putting every note in the prompt. The main agent saves Markdown notes in user and project stores; a background recall selects relevant notes from their descriptions and attaches them to a tool result if ready. The turn never waits for recall.

  ```text
  [User and project notes] --> [Background recall]
                                      |
                                      v (when ready)
                               [Tool-result context]
  ```

- [Project instructions](concepts/harness-channels.md) — Keeps repository conventions in files so they can be reviewed alongside code. San reads user instructions, then the `AGENTS.md` chain from repo root to working directory, plus rules and local overrides. It sends them as reminders under a shared size limit.

  `User files + project files + local overrides -> bounded reminders`

- [Self-learning](packages/selflearn.md) — Turns lessons about how to work into reusable skills rather than memory notes. Off by default, it starts a background reviewer with skill-management tools only after an eligible `Evolve` request from a completed turn. It can update agent-owned skills but cannot edit user-authored ones.

  `Completed turn + Evolve -> background reviewer -> agent-owned skills`

## Agent and session collaboration

- [Subagents](packages/subagent.md) — Delegation separates a job from the parent's conversation. The `Agent` tool starts a worker with its own context and effective tool permissions. Foreground work returns a tool result; background work gets a task ID so the parent can continue.

  `Agent call -> separate worker -> foreground result or background task`

- [Agent messaging](packages/broker.md) — Lets agents in one San process exchange messages without sharing a conversation. The broker routes to the main agent or a registered worker task ID. Ordinary messages need a recipient that can accept them; child completions can wait in memory for their owning worker.

  `Addressed message -> in-process broker -> recipient inbox`

- [Session groups](design/proposal-0003-session-groups.md) — Lets independent San sessions collaborate on the same machine without sharing a process. `/group` registers members in a roster on disk. Sending writes to a member's inbox directory, so messages can wait while the recipient is offline.

  `[Sending session] --> [Member inbox on disk] --> [Receiving session]`

- [Flow: collaboration blueprints](packages/flow.md) — Declares which steps depend on each other instead of relying on one agent to remember the order. The scheduler can launch independent ready steps through San's existing task system. Step checkpoints let retries or resumes reuse completed work outside the rerun scope.

  ```text
  [Blueprint] --> [Scheduler]
                       +--> [Ready step A] --> checkpoint A
                       +--> [Ready step B] --> checkpoint B
  ```

## Sessions, tasks, and automation

- [Sessions](packages/session.md) — Uses committed event batches as the source of truth rather than storing only the latest prompt. Validated replay reconstructs conversation history and the active model context. Compaction changes what the model sees on the next turn without erasing the history.

  `Committed events -> validated replay -> history and active context`

- [Tasks and processes](packages/task.md) — Tracks actual running work separately from the agent's planning todos. Shell jobs, workers, and schedulers are tied to their launching session; the manager tracks lifecycle state and streams shell output to task logs. The UI can inspect or stop live work.

  `Background work -> session-owned task -> status, output, or stop`

- [Autopilot](guides/autopilot.md) — Lets a side model take over selected decisions, not the whole permission policy. It can weigh eligible approvals; explicit denials and safety limits still apply. At turn end, a running mission within its limit can queue another instruction if the judge recommends continuing.

  `Turn ends -> mission and limit check -> judge -> next instruction or wait`

- [Scheduling](packages/cron.md) — Turns a time trigger into an ordinary conversation turn. While San runs, its scheduler checks which cron jobs are due; the UI submits a prompt when idle or queues it when busy. Durable job definitions survive restart, but there is no separate daemon.

  `Cron tick -> due prompt -> run now or queue until idle`

## Tools and extensions

- [Built-in tools](packages/tool.md) — Gives the model named operations with structured arguments instead of direct access to the runtime. San exposes tool schemas, checks permissions on a requested operation, and dispatches it to the registered implementation. The result goes back into the conversation.

  `Schema -> model call -> permission gate -> tool result`

- [Web search](packages/search.md) — Separates web queries from any one search backend. `/search` selects the provider, with Exa as the default. Each adapter handles its own request format and returns the same title, URL, and snippet shape to `WebSearch`.

  `Query -> selected provider -> normalized results`

- [Skills](packages/skill.md) — Keeps reusable procedures out of the default context until needed. San discovers `SKILL.md` files and advertises only metadata for active skills. Invocation loads the full instructions and points the agent to bundled resources.

  `SKILL.md -> metadata catalog -> full instructions on invocation`

- [Plugins](packages/plugin.md) — Packages related extensions so they can be installed and enabled together. The plugin registry tracks enablement by scope; only enabled plugins contribute skills, agents, commands, MCP servers, and hooks to their corresponding loaders.

  `Plugin bundle -> scoped enablement -> extension loaders`

- [MCP](packages/mcp.md) — Reuses the agent's tool interface for programs outside San. The manager connects to enabled servers over stdio or HTTP, discovers their tool schemas, and exposes namespaced tools to the agent. An allowed call is routed back to the server that advertised it.

  `Discover schemas from server -> expose tools -> route allowed calls back`

- [Hooks](packages/hook.md) — Adds behavior at lifecycle events without changing the agent loop. The engine matches handlers against the event and its input, such as a tool name. Synchronous results can affect whether an operation continues; async hooks run separately.

  `Lifecycle event -> matching handlers -> sync outcome or async work`

- [Commands](packages/command.md) — Provides one slash palette for both app actions and reusable prompts. The registry combines built-in handlers, dynamic entries, and custom Markdown commands. At invocation, San dispatches a handler or expands the prompt and submits it to the agent.

  `Slash input -> registry lookup -> handler or expanded prompt`

## Models, behavior, and permissions

- [Models and providers](packages/llm.md) — Separates model choice and credentials from the agent loop. `/models` manages provider connections and model selection. At inference time, a shared client invokes the resolved provider and streams its response back to the runtime.

  `Connections and model selection -> inference client -> provider stream`

- [Personas](concepts/persona.md) — Switches how the agent works without starting a new conversation. A persona supplies prompt parts, skills, and a settings overlay. Prompt and skill changes reach the next inference; tool and permission changes take effect when the agent is rebuilt.

  `Select persona -> prompt and skills now / tool policy on rebuild`

- [Settings](packages/setting.md) — Separates personal defaults from shared repo policy and local overrides. San reads scoped files, limits what untrusted project files can contribute, then merges fields according to their semantics. For example, restrictive permission rules can accumulate rather than being replaced by a higher-priority file.

  `Scoped configuration -> workspace trust filter -> field-specific merge`

- [Permissions](concepts/permission-model.md) — Separates a model's request from authority to execute it. The policy evaluates explicit rules, safety checks, session grants, and mode defaults in order. It returns allow, deny, or ask; if approval is still required and nobody can answer, the call is refused.

  ```text
  Tool request --> [Permission policy]
                           +-- allow --> execute
                           +-- deny  --> stop
                           +-- ask   --> approval, or deny if unavailable
  ```

## Interaction and inspection

- [CLI](guides/getting-started.md) — Offers both interactive work and one-shot use from scripts. `san` opens the terminal UI, where turns can pause for input or approvals. `san -p` or piped input selects the headless path, which runs one request and writes the answer to stdout.

  `Input -> interactive UI or one-shot run -> stdout for print mode`

- [ACP: Agent Client Protocol](packages/app.md#acp-stdio-host) — Separates the client interface from San's native agent runtime. A compatible desktop client can start `san acp` over stdin/stdout to send prompts, receive streamed text and tool progress, and answer approval requests. A phone or other remote client can use the same integration if it provides a bridge to the machine running San. `san acp` itself does not expose a network endpoint or synchronize live sessions across devices.

  ```text
  [Desktop ACP client] ---------------------------+
  [Phone or remote client] --> [External bridge] -+
                                                  |
                                                  v
                                        [san acp / stdio]
                                                  |
                                                  v
                                        [San runtime on host]
  ```

- [Inspector](packages/inspector.md) — Reads the original run record instead of maintaining a separate debug history. `san inspector` serves a read-only browser UI on loopback, using the native store to replay committed events and reconstruct requests. New committed batches reach the browser over SSE.

  `Native event store -> replay and SSE -> local browser`
