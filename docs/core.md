# Core Agent Architecture

## Agent Construction

```
┌─────────────────────────────────────────────────────────────┐
│  core.NewAgent(Config)                                      │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │   LLM    │  │  System   │  │  Tools   │                  │
│  │ (stream) │  │ (layers)  │  │(registry)│                  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘                  │
│       │              │             │                        │
│       └──────────────┴─────────────┘                        │
│                          │                                  │
│                     ┌────┴────┐                              │
│                     │  Agent  │                              │
│                     │         │                              │
│               Inbox ◄────────►  Outbox                      │
│             (chan Message)  (chan Event)                      │
│                     └─────────┘                              │
│                                                             │
│  Optional: CWD, MaxTurns, CompactFunc                       │
│                                                             │
│  core.Agent has NO dependency on hooks.                     │
│  Hooks are an app-layer concern — see hook.md.              │
└─────────────────────────────────────────────────────────────┘
```

## Execution Model

`ThinkAct` is the agent's atomic operation — one full inference-action cycle.

```
                      ThinkAct(ctx) → *Result
                      ┌──────────────────────────────────┐
                      │  PreInfer ──► LLM stream          │
                      │                  │                │
                      │             PostInfer             │
                      │                  │                │
                      │            tool calls?            │
                      │             │       │             │
                      │            Yes      No            │
                      │             │       │             │
                      │        execTools  return Result   │
                      │             │                     │
                      │        loop back to PreInfer      │
                      └──────────────────────────────────┘
                           ▲                    ▲
                           │                    │
            ┌──────────────┘                    └──────────────┐
            │                                                  │
     Run() — TUI                                    Direct call — Subagent
     ┌─────────────────┐                            ┌─────────────────────┐
     │  loop:          │                            │  ag.Append(prompt)  │
     │    waitForInput │                            │  ag.ThinkAct(ctx)   │
     │    ingest ──┐   │                            │                     │
     │    ThinkAct │   │                            │  ag.Append(followUp)│
     │    emit     │   │                            │  ag.ThinkAct(ctx)   │
     │  until Stop │   │                            │  ...                │
     └─────────────┘   │                            └─────────────────────┘
                       │
                       └──► both paths: append to conversation history
```

## Run Loop (TUI path)

```
          Inbox                              Outbox
            │                                  ▲
            ▼                                  │
┌───────────────────────────────────────────────────────────┐
│                                                           │
│   ┌─────────┐                                             │
│   │  WAIT   │◄──────────────────────────────────┐         │
│   │ (block) │                                   │         │
│   └────┬────┘                                   │         │
│        │ message arrives                        │         │
│        ▼                                        │         │
│   ┌─────────┐                                   │         │
│   │  DRAIN  │  non-blocking drain of            │         │
│   │         │  accumulated messages             │         │
│   └────┬────┘                                   │         │
│        ▼                                        │         │
│   ┌──────────────────────────────────────┐      │         │
│   │        ThinkAct(ctx) → Result        │      │         │
│   │                                      │      │         │
│   │  PreInfer ──► LLM stream ──► PostInfer      │         │
│   │                                │     │      │         │
│   │                          tool calls? │      │         │
│   │                           │       │  │      │         │
│   │                          Yes      No │      │         │
│   │                           │       │  │      │         │
│   │                           ▼       │  │      │         │
│   │                      ┌─────────┐  │  │      │         │
│   │                      │execTools│  │  │      │         │
│   │                      │ Gate    │  │  │      │         │
│   │                      │ Execute │  │  │      │         │
│   │                      │ Record  │  │  │      │         │
│   │                      └────┬────┘  │  │      │         │
│   │                           │       │  │      │         │
│   │                      loop back    │  │      │         │
│   │                      to PreInfer  │  │      │         │
│   │                                   │  │      │         │
│   │                              OnTurn ─┘      │         │
│   └──────────────────────────────────────┘      │         │
│                                    │            │         │
│                                    └────────────┘         │
│                                                           │
│   SigStop / ctx.Done ──► OnStop ──► return                │
└───────────────────────────────────────────────────────────┘
```

## Tool Execution

core.Agent knows nothing about hooks — it only sees `core.Tools` (which
may be wrapped with a permission decorator). For hook integration around
tool execution, see [permission.md](permission.md#hook-integration).

```
  tool calls from LLM
        │
        ▼
  ┌─── EMIT + RESOLVE (sequential) ─────────────┐
  │  for each call:                              │
  │    emit PreTool event (outbox)               │
  │    tools.Get(name) → tool (or nil → error)   │
  └──────────────────────────────────────────────┘
        │
        ▼
  ┌─── EXECUTE (parallel) ──────────────────────┐
  │  tool.Execute(ctx, params)                   │
  │    └─ if wrapped by WithPermission:          │
  │       IsSafeTool? → skip check               │
  │       PermissionFunc → Permit/Reject/Prompt  │
  │       Prompt → blocks on PermissionBridge    │
  │  panic recovery per goroutine                │
  └──────────────────────────────────────────────┘
        │
        ▼
  ┌─── RECORD (sequential) ─────────────────────┐
  │  append ToolResult to conversation           │
  │  emit PostTool event (outbox)                │
  └──────────────────────────────────────────────┘
```

## System Prompt

`core.System` is a layered structure of named **Sections**, each in a fixed
**Slot**. The agent calls `system.Prompt()` once per inference; the result
is cached and only rebuilt when a section is mutated (`Use` / `Drop` /
`Refresh`).

```
core.System
├── slot 0: identity      ─┐
├── slot 1: provider       │
├── slot 2: policy         │  stable cache prefix
├── slot 3: guidelines     │
├── slot 4: memory         │
├── slot 5: capabilities  ─┘
├── slot 6: invocation       per-turn
├── slot 7: environment   ─┐
└── slot 8: notice        ─┘  volatile (date, hooks)
                │
                ▼
        System.Prompt()
   (cached, rebuilds on mutation)
```

See [System Prompt](system-prompt.md) for full slot semantics, build API,
XML envelopes, and identity/skill/agent injection paths.

## Outbox Events

core.Agent emits events to its Outbox channel at each lifecycle point.
The TUI observes these for rendering. **These are NOT hook events** —
hooks are an app-layer concern (see [hook.md](hook.md)).

```
  Agent Lifecycle                  Outbox Event            TUI Action
  ──────────────────────────────────────────────────────────────────────────

  Run() starts
    │
    ▼
  waitForInput()
    │ message arrives
    │
    ▼
  ThinkAct() loop
    │
    ├─► PreInfer ················· start stream spinner
    │
    ▼
  streamInfer()
    │
    ├─► OnChunk (per token) ······ append to streaming view
    │
    ├─► PostInfer ················ update token counts, set tool calls
    │
    ▼
  execTools()
    │
    │  for each tool call:
    │    ├─► PreTool ············· show "building tool" status
    │    │  tool.Execute()
    │    └─► PostTool ············ append tool result, apply side effects
    │
    ▼
  end of turn?
    ├─ tool calls → loop back to PreInfer
    └─ no calls
         ├─► OnTurn ·············· commit messages, save session, drain queues
         └─► back to waitForInput()

  Run() returns
    └─► OnStop ··················· cleanup agent session
```

Hook events (PreToolUse, PostToolUse, Stop, etc.) are fired by the
app layer in response to these outbox events — not by core.Agent itself.

## Auto Compaction

Compaction is a **core.Agent** responsibility. The agent checks context usage
before each inference (pre-infer check in ThinkAct loop). When context exceeds
95% of the input limit, the agent calls its `CompactFunc` to summarize the
conversation and replaces its internal messages with the summary.

```
  core.Agent ThinkAct() loop:
    │
    ├─ pre-infer: tokensIn >= 95% of InputLimit?
    │   │
    │   yes ──► agent.compact()
    │   │       ├─ CompactFunc(ctx, msgs) → summary
    │   │       ├─ SetMessages([FormatCompactSummary(summary)])
    │   │       └─ emit OnCompact event
    │   no  ──► continue to inference
    │
    ▼

  TUI: HandleAgentCompact(info):
  ┌────────────────────────────────────────────────────┐
  │  1. conv.Clear() — wipe TUI display messages       │
  │  2. Inject summary as user message                 │
  │  3. Fire PostCompact hook                          │
  │  4. Agent continues with compacted context         │
  └────────────────────────────────────────────────────┘

  Manual /compact: uses CompactCmd → HandleCompactResult.
  Stops the agent; next user message restarts with
  compacted messages from m.conv.
```
