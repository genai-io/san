# Subagent System

## Core Concept

The Main Agent runs in the TUI, driving an LLM loop:

```
User input → LLM inference → Tool execution → LLM inference → ... → end_turn → Wait for next input
```

The Main Agent can spawn Subagents via the `Agent` tool to delegate work. Each Subagent is a full LLM loop with its own conversation, tool set, and system prompt. Subagents start with a fresh context — the parent must put all needed background into `AgentRequest.Prompt`. See [System Prompt → Subagent identity replacement](system-prompt.md#subagent-identity-replacement) for how the subagent's identity slot is built from its `AgentConfig`.

There are exactly two interaction patterns:

```
                        ┌─────────────┐
                        │    User     │
                        └──────┬──────┘
                               │ input
                               ▼
                        ┌─────────────┐
                        │ Main Agent  │
                        │  LLM loop   │
                        └──┬──────┬───┘
                           │      │
              foreground   │      │  background
              (synchronous)│      │  (async)
              ▼                      ▼
     ┌────────────────┐    ┌────────────────┐
     │  Subagent A    │    │  Subagent B    │ goroutine
     │  Blocks parent │    │  Runs alone    │
     │  Result = tool │    │  Done → Hub│
     └────────────────┘    └────────────────┘
```

## Pattern 1: Foreground — Synchronous Call

The Subagent runs inside the Main Agent's tool execution. The Main Agent blocks until the Subagent finishes, and the result is returned directly as the `Agent` tool's output. No different from calling `Read` or `Grep`.

```
Main Agent LLM loop
│
├─ LLM inference → "Need to search the codebase"
│
├─ Tool call: Agent(prompt: "find all API endpoints")
│     │
│     ▼
│   Executor.Run()
│     ├─ Build core.Agent (system prompt + tool set)
│     ├─ agent.ThinkAct(ctx)
│     │     ├─ LLM → Grep → LLM → Read → LLM → end_turn
│     │     └─ Returns Result{Content: "Found 12 endpoints..."}
│     └─ Return Content as tool result
│
├─ Main Agent receives tool result: "Found 12 endpoints..."
├─ LLM continues next reasoning step
```

## Pattern 2: Background — Hub

All inter-agent communication flows through a central **Hub**. Producers call `hub.Publish(event)`. The Hub calls the subscriber's delivery function. That's it.

Task completion is just one event type. SendMessage, cron, hooks — all the same `Publish()` call.

### Event

Inspired by [CloudEvents](https://github.com/cloudevents/spec):

```go
type Event struct {
    Type    string    // "task.completed", "agent.message", "cron.fired"
    Source  string    // producer: "agent:<id>", "system:cron"
    Target  string    // consumer: "agent:<id>", "main"
    Subject string    // human-readable: "fix auth module completed"
    Data    string    // payload: XML content, message text, etc.
    Time    time.Time
}
```

### Hub

The Hub is pure pub/sub: `map[string]func(Event)`. Three methods, ~30 lines.

```go
package hub

type Hub struct {
    mu   sync.RWMutex
    subs map[string]func(Event)
}

func New() *Hub
func (h *Hub) Register(id string, deliver func(Event))
func (h *Hub) Unregister(id string)
func (h *Hub) Publish(e Event)    // routes to target's delivery function
```

Routing only. No buffering, no channels, no goroutines inside. Each consumer decides how to receive — the Hub just calls the delivery function.

### Register

Each consumer registers a delivery function. The function captures whatever the consumer needs — inbox, program, hooks — via closure. No intermediate types.

**Background agent — deliver directly to existing inbox:**

```go
hub.Register("agent:"+taskID, func(e Event) {
    agent.Inbox() <- core.Message{Role: core.RoleUser, Content: e.Data}
})
```

No bridge goroutine. No second channel. The agent already has a buffered inbox (16) — just write to it. The agent reads at the next turn boundary via `drainInbox()`.

**Main agent — two-stage pipeline via `mainEvents`:**

The main agent runs inside Bubble Tea's single-threaded MVU loop, so it cannot consume raw `hub.Event` directly in its `core.Agent.Inbox`. Instead, a separate Go channel (`mainEvents`) acts as a staging buffer between the Hub and the Agent Inbox:

```
hub.Publish()
    │
    ▼
mainEvents (chan hub.Event)        ← raw events: Type, Source, Subject, Data
    │
    │  drainTurnQueues() at turn boundary:
    │  ① Priority: user queue > cron > async hooks > mainEvents
    │  ② Batch: drain up to 8 events at once
    │  ③ Format: hub.Merge() → XML-wrapped content
    │  ④ Two messages: RoleNotice (TUI display) + RoleUser (LLM reasoning)
    │
    ▼
core.Agent.Inbox (chan core.Message)  ← LLM-consumable: Role, Content
    │
    ▼
LLM inference
```

```go
m.mainEvents = make(chan hub.Event, 64)
hub.Register("main", func(e Event) {
    m.mainEvents <- e
})

// In drainTurnQueues — lowest priority, after user/cron/hooks:
if events := drainEvents(m.mainEvents, maxEventsPerDrain); len(events) > 0 {
    msgs := eventsToMessages(events)
    return m.injectNotification(hub.Merge(msgs)), true
}
```

Why not write directly to `core.Agent.Inbox` like background agents do? Three reasons:
- **Batching**: 3 tasks completing concurrently → 3 raw events → merged into 1 LLM message (1 inference, not 3)
- **Priority ordering**: user input is always processed first; task completions wait
- **TUI notice**: each notification produces a visible `RoleNotice` ("fix auth completed") alongside the `RoleUser` XML payload for the LLM

**Unregister — on completion or kill:**

```go
hub.Unregister("agent:" + taskID)    // just delete(map, id)
```

No channel to close, no goroutine to stop. Just remove the function from the map.

### Publish

Any code that calls `hub.Publish()` is a producer. No observer structs — closures capture what they need:

```go
// ── At app startup (wireTaskLifecycle) ──

task.SetLifecycleHandler(taskLifecycleFunc{
    onCompleted: func(info task.TaskInfo) {
        hookEngine.ExecuteAsync(hook.TaskCompleted, hookInput)
        tracker.CompleteWorker(trackerSvc, info)
        eventHub.Publish(hub.Event{
            Type:    "task.completed",
            Source:  fmt.Sprintf("agent:%s", info.ID),
            Target:  "main",
            Subject: msg.Notice,
            Data:    msg.Content,
        })
    },
})

// ── SendMessage tool ──

hub.Publish(Event{
    Type:   "agent.message",
    Source: "agent:" + senderID,
    Target: "agent:" + recipientID,
    Data:   content,
})

// ── Cron scheduler ──

hub.Publish(Event{
    Type:   "cron.fired",
    Source: "system:cron",
    Target: "main",
    Data:   prompt,
})
```

### Architecture

```
 Producers                          Hub                         Consumers
 ─────────                     ──────────────                   ─────────

 task.OnCompleted(func{        ┌────────────┐
   hub.Publish(Event{          │            │    Register("main", func(e) {
     Target: "main"        ───►│            ├──►   m.events <- e     ← consumer channel
   })                          │            │    })
 })                            │            │      drainTurnQueues():
                                │    Hub     │      ├─ Show e.Subject
 SendMessage tool:              │            │      └─ Inject e.Data → agent
   hub.Publish(Event{          │ map[string] │
     Target: "agent:B"    ───►│  func(Event)│    Register("agent:B", func(e) {
   })                          │            ├──►   agent.Inbox() <- msg  ← direct write
                                │            │    })
 Cron scheduler:                │            │
   hub.Publish(Event{          │            │
     Target: "main"        ───►│            │
   })                          └────────────┘
```

No channels, no goroutines inside the Hub. Each delivery function writes to whatever the consumer already has — a Go channel for the main agent, an agent inbox for background agents.

### Background Agent Flow

```
 Agent Tool                 Background Goroutine                TUI Update Loop
 ──────────                 ────────────────────                ───────────────

 Agent(bg:true)
 ├─ Build subagent
 ├─ hub.Register("agent:A",
 │    func(e) { agent.Inbox() <- ... })
 ├─ Spawn goroutine ──────► ThinkAct()
 ├─ Return taskID             ├─ LLM → tools → ...
 │                            └─ end_turn
 │  TUI Update:                     │
 │  Tracker.Add(id, ◷)            │
 │                                  ├─ hub.Publish(Event{
 │  Main Agent                      │     Type:   task.completed
 │  continues working               │     Source: agent:A
 │                                  │     Target: main
 │                                  │     ...
 │                                  │   })
 │                                  └─ hub.Unregister("agent:A")
 │
 │                                  Hub calls subs["main"](e)
 │                                    = m.events <- e   (channel)
 │
 │  Main Agent next turn:
 │  drainTurnQueues → drain(m.events):
 │    ├─ CompleteWorker (tracker)
 │    ├─ Notice: e.Subject
 │    └─ Inject e.Data → LLM reasons about result
```

### Agent-to-Agent Communication

Same `Publish()` call, different target:

```
 Agent A                              Hub                        Agent B
 ───────                         ──────────                      ───────

 SendMessage(to: "agent:B",
   content: "check tests")
   │
   └─ hub.Publish(Event{
        Type:   agent.message
        Source: agent:A               Hub calls subs["agent:B"](e)
        Target: agent:B          ───►   = agent.Inbox() <- msg   ← direct write
        Data:   "check tests"
      })                                                          drainInbox:
                                                                  sees "check tests"
                                                                  LLM reasons about it
```

### Tracker

Background task display state. Managed via standalone functions in `task/tracker/background.go`:

```
On launch:      tracker.TrackWorker(trackerSvc, launch)     → show ◷ spinner
On completion:  tracker.CompleteWorker(trackerSvc, info)     → show ✓ or ✗
```

`TrackWorker` is called from `trackAgentLaunch()` when the Agent tool returns a background task. `CompleteWorker` is called from the `wireTaskLifecycle()` closure on task completion.

### Batching

The channel buffers concurrent completions. `drainTurnQueues()` does a non-blocking drain of up to `maxEventsPerDrain` events, merging them into a single notification:

```go
if events := drainEvents(m.events, maxEventsPerDrain); len(events) > 0 {
    msgs := eventsToMessages(events)
    return m.injectNotification(hub.Merge(msgs)), true
}
```

Concurrent completions naturally merge. No tick, no polling.

### Two-Message Injection

Each event delivery becomes two messages in the TUI conversation:

```
RoleNotice:  e.Subject → user sees ("fix auth completed")
RoleUser:    e.Data    → LLM sees (<task-notification ...>)
```

## Lifecycle

```
CONSTRUCT → EXECUTE → RETURN → (HUB) → CLEANUP
```

- **CONSTRUCT**: Resolve agent type → Build system prompt + tool set → Create core.Agent → Optional git worktree isolation
- **EXECUTE**: Inject prompt → ThinkAct loop (LLM inference → tool execution → repeat until end_turn)
- **RETURN**: Foreground → return tool result directly; Background → store in AgentTask
- **HUB** (background only): Complete → `hub.Publish()` → delivery function → target agent
- **CLEANUP**: `hub.Unregister()` → Save session → Close MCP → Delete worktree if no changes

## What Subagents Inherit

Principle: **Inherit identity and capabilities, isolate state and context.**

| Inherited | Reason |
|---|---|
| LLM provider + model | Needs the same (or specified) LLM for inference |
| User instructions (GEN.md) | User preferences apply to all agents |
| Project instructions | Project conventions apply to all agents |
| Hook engine | Subagent tool calls must also trigger hooks |
| MCP connections | Subagent can use parent's MCP services |
| Working directory | Same repository (unless worktree-isolated) |

| Isolated | Reason |
|---|---|
| Conversation history | Subagent starts fresh; parent context is noise |
| File cache | Independent reads avoid stale data |
| Task state | Independent task tracking |

## Agent Types

Agent types are declarative specialization definitions — markdown files with YAML frontmatter:

```yaml
---
name: go-reviewer
description: Reviews Go code for correctness and idioms
tools: [Read, Glob, Grep, Bash]
model: inherit
permission-mode: explore
maxTurns: 50
---

You are a Go code reviewer. Focus on correctness bugs...
```

Source priority: Built-in → Project `.gen/agents/*.md` → User `~/.gen/agents/*.md` → Plugin agents

## Related Tools

| Tool | Purpose |
|------|---------|
| `Agent` | Spawn a subagent (foreground or background) |
| `SendMessage` | Send a follow-up message to a completed agent from its saved session |
| `SendMessage` | Send a message to a running/completed agent |
| `TaskOutput` | Read output from a background task |
| `TaskStop` | Stop a running background task |

## Design Principles

1. **Prompt is the interface.** Subagents cannot see the parent's conversation — the prompt must be self-contained.
2. **Completed work becomes a message.** Background results are injected as a user message — no special protocol, no shared state, just a message in the conversation.
3. **Hub is pure pub/sub.** `map[string]func(Event)`, ~30 lines. Three methods: `Register`, `Unregister`, `Publish`. No buffering, no channels inside — routing only.
4. **Consumer owns the buffer.** Main agent uses a Go channel; background agents use their existing inbox. Each subscriber's delivery function decides how to receive. No intermediate types.
5. **Turn-boundary delivery.** Main agent's channel accumulates events, drained in `drainTurnQueues()`. Concurrent completions merge naturally. No tick-based polling.
6. **Isolated by default.** Independent context, file cache, and task state. Only share what's necessary (provider, instructions, hooks).
7. **Fail independently.** A Subagent failure produces an error result — it doesn't crash the parent. The parent decides how to handle it.
8. **Agent types are data, not code.** Markdown + frontmatter definitions — extensible without recompilation.
