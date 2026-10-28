---
package: github.com/genai-io/san/internal/broker
layer: feature
---

# broker

Routes messages between agents. The small piece that sits between a sender and
a recipient so neither has to know how to reach the other — the whole of
inter-agent communication, in ~60 lines.

## Model

- An agent **Registers** an address to receive at when it starts (the main
  conversation under `Main`, a subagent execution under its stable task id) and
  **Unregisters** when it stops.
- Anyone **Sends** a `Message` stamped with a destination (`To`). The broker
  delivers it to whoever holds that address; a message to an address no one
  holds is dropped.

Direct addressing only — no topics, no broadcast, no queuing beyond the
recipient's own. What a delivered message does (enter a subagent's
conversation, wake the main loop) is the recipient's business, kept out of this
package so the broker only routes.

## Contract

```go
package broker

const Main = "main"

type Message struct {
	SessionID string // owning conversation for local worker messages
	From    string // sender address
	To      string // recipient address
	Subject string // short human-facing notice (may be empty)
	Content string // body delivered to the recipient
}

func Register(addr string, deliver func(Message) bool) // add a recipient
func Unregister(addr string)                      // remove it (idempotent)
func Send(m Message) bool                          // routes to m.To; false when no one holds it
func Reset()                                       // test-only
```

Addresses identify logical agents — a task id (`generateShortID()`) or `Main` — so
an address is held by exactly one agent at a time; `Register`/`Unregister` are
a plain add/remove on the map. The registry is a process-wide singleton, like
`task.Default()`. The delivery function runs outside the lock and must not
block (it enqueues for the recipient and returns).
Worker messages and completion notices carry their launching session. Main
rejects a different session both before enqueueing and before applying a queued
notice, so a switch cannot inject an old worker's result into the new session.

## Who uses it

| Sender | Sends |
|---|---|
| task lifecycle (`app`) | a background task's completion to `Main` |
| `SendMessage` tool | main → a running subagent, or a subagent → `Main` |

| Recipient | Registers |
|---|---|
| main loop (`app`) | `Main` → forwards onto the main-loop notice channel |
| each active subagent execution (`subagent.Executor`) | its task id → `core.Agent.Append`, heard at its next step; its run does not end with one unread |

A background task's **completion** is pushed automatically when its run ends.
Main injects it immediately while idle, or at the next turn boundary during an
active stream; it never polls for output. `SendMessage` uses this broker for active
executions. For an idle or recoverable failed worker, the tool checks the owning
main conversation's Agent permission and starts another background execution in
its existing session; the broker itself neither restores sessions nor creates
workers. Stopped/killed agents and unknown local IDs fail without replacement.

Acceptance is not acknowledgment or task completion. The executor closes inbox
acceptance atomically with its final pending-input check, preserving accepted
input in history even on failure. Main receives a final result through the
foreground tool result or background completion notification.

## See Also

- Subagents: [`subagent`](subagent.md)
- Background tasks: [`task`](task.md)
- Layer: `feature`
