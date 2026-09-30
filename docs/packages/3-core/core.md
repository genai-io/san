---
package: github.com/genai-io/san/internal/core
layer: core
---

# core

The agent primitive: the `Agent` interface, its surrounding `System` /
`Tools` / `LLM` contracts, and the message/event types they exchange. No
implementations live here — only the contracts every feature package
shares.

## Purpose

Everything in `feature` and above depends on this package; nothing here
depends on anything outside `internal/log`, `context`, and stdlib. Keeping
the surface small and stable is the whole point.

This is also the only package that gets multiple interfaces on one page —
`Agent`, `System`, `Tools`, `Tool`, `LLM` are the system's primitives and
move together when they move at all.

## Contract

### Agent

```go
package core

// Agent — an LLM in a loop. Three capabilities: System (WHO), Tools (WHAT),
// Append/Inbox/Outbox (HOW it communicates).
type Agent interface {
    ID() string
    System() System
    Tools() Tools
    Inbox() chan<- Inbound    // control signals only; caller owns and closes
    Outbox() <-chan Event     // agent owns and closes on Run() return
    Messages() []Message
    SetMessages(msgs []Message)
    Append(msg Message)       // queue a message; any goroutine, never blocks
    ThinkAct(ctx context.Context) (*Result, error)
    Run(ctx context.Context) error
    InterruptCurrentTurn() <-chan struct{}
}

func NewAgent(cfg Config) Agent  // returns interface — see Note below
```

### System

```go
// System — the composable, mutable system prompt.
type System interface {
    Prompt() string
    Use(sec Section, caller string)
    Drop(name, caller string)
    Refresh(name, caller string)
    Sections() []Section
    SetObserver(fn func(SystemChange))
}
```

### Tools

```go
// Tool — one capability the agent can execute. Pure (no hooks, no permissions).
type Tool interface {
    Name() string
    Description() string
    Schema() ToolSchema
    Execute(ctx context.Context, input map[string]any) (string, error)
}

// Tools — mutable collection of Tool.
type Tools interface {
    Get(name string) Tool
    All() []Tool
    Add(tool Tool, caller string)
    Remove(name, caller string)
    Schemas() []ToolSchema
    SetObserver(fn func(ToolsChange))
}
```

### Reaching the model

There is no inference interface. The loop holds the SDK's own client and
ranges its stream, and what the application still owns is supplied as three
narrow functions on `Config`:

```go
// Which client answers this turn — a function of the conversation, because one
// endpoint's headers depend on what the turn sends (Copilot's vision opt-in).
Client func(msgs []Message) (*ai.Client, error)
// The settings a person can change mid-session — the reasoning rung, the
// output cap — asked for fresh on every inference.
CallOptions func() []ai.Option
// The prompt size at which auto-compaction fires: the window less the
// reply's room. Zero turns it off.
ContextBudget func() int
```

There is nothing left to translate: a message, a tool, a tool result and a
response are all the SDK's types, and `ai.IsRetryable` / `ai.IsContextExceeded`
answer about a failure directly. What core still converts is one thing —
`ToAITools`, in `tool.go`, because San holds a schema without a Run.

### Known Violations

The contracts here are mostly clean (this package is the *design intent*),
but a few items deserve flagging:

- **Rule 1 (small) — `Agent` has 8 methods.** Borderline. The methods
  cluster into identity (`ID`/`System`/`Tools`), I/O (`Inbox`/`Outbox`),
  state (`Messages`/`SetMessages`/`Append`), and execution
  (`ThinkAct`/`Run`). A clean split would yield `AgentIdentity`,
  `AgentIO`, `AgentMessages`, `AgentRunner` — but `Agent` is the central
  primitive and downstream code treats it as one cohesive value. Document
  the trade-off; don't split.
- **Rule 1 — `System` has 6 methods, `Tools` has 6.** Same trade-off:
  observer + mutation + query on one type. Acceptable for now.
- **Rule 5 (constructors return concrete types).** `NewAgent` returns
  `Agent` (interface). The concrete `*agent` is unexported, so callers
  *must* use the interface — there is no concrete type to return.
  Acceptable: this is the *only* place an interface return is the right
  call, because hiding the implementation is the whole point of the
  primitive.

`Tool` (4 methods) and `LLM` (2 methods) are model-citizen interfaces and
need no changes.

## Internals

There are no business-logic internals to document — implementations live
in `internal/agent` (for `core.Agent`), `internal/core/system/` (for
`System`), `internal/tool/` (for `Tool`/`Tools`), and
`internal/llm` (for `LLM`).

Three implementation files back `NewAgent`, kept inside `core` because the
loop is inseparable from the contract:

- `run.go` — the agent: the loop that waits for work and the exchange it hands
  it to. Messages have one queue, the SDK agent's: `Append` adds to it, and the
  SDK enters them at the next step boundary — or opens the next exchange with
  them — and does not end an exchange while any wait. `Run` only needs a wake
  to start an exchange when idle. The inbox carries control signals (stop,
  manual compaction), which act between turns on the agent's goroutine. Both
  halves are one file because they are one struct — every line of the exchange
  reaches into the loop's fields, and the loop calls the exchange.
- `toolset.go` — what the model may call, and how a tool learns which call it
  is running. The exchange asks for it once, through `offered`, and never looks
  inside.
- `compact.go` — shortening a conversation that has outgrown its window. Two of
  the exchange's hooks ask for it and `/compact` asks for it through a signal,
  so it belongs to neither.

## Lifecycle

`NewAgent` panics if `LLM`, `System`, or `Tools` is nil. After
construction, callers own the `Inbox` channel (must close when done
sending) and read the `Outbox` until it closes (agent owns it).

`Append` is how every message arrives — a user's, a finished background task's,
a steer to a subagent — from any goroutine, mid-turn or idle. Mid-turn it lands
before the next inference; idle it wakes `Run`. A message queued during a turn
the user interrupts waits for the next message rather than restarting the agent.

`Run` returns when the context is cancelled or `SigStop` arrives. After `Run`
returns, sending to the inbox blocks indefinitely.

## Tests

```
internal/core/run_test.go           — loop behaviour: signals, mid-turn
                                      delivery, the interrupt latch.
internal/core/exchange_test.go      — one exchange: compaction, the toolset it
                                      offers, the client it asks for.
internal/core/agent_content_test.go — what one exchange produces and reports.
internal/core/aidriver_test.go      — a scripted driver, so a test says what
                                      the endpoint sends and nothing between
                                      has to be stubbed.
internal/core/backoff_test.go       — the backoff the two non-agent retry
                                      loops share.
internal/core/message_test.go       — message value equality and copying.
```

## See Also

- Code: `internal/core/`
- Consumer: [`packages/agent.md`](../2-feature/agent.md) (`internal/agent` wraps `core.Agent`)
- Subsystem implementations: [`packages/tool.md`](../2-feature/tool.md), [`packages/llm.md`](../2-feature/llm.md), [`packages/subagent.md`](../2-feature/subagent.md)
- Layer: `core` (see [`reference/dependency-rules.md`](../../reference/dependency-rules.md))
