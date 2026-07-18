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
  conversation under `Main`, a background subagent under its task id) and
  **Unregisters** when it stops.
- Anyone **Sends** a `Message` stamped with a destination (`To`). The broker
  delivers it to whoever holds that address; a message to an address no one
  holds is dropped.

Direct addressing only — no topics, no broadcast, no queuing beyond the
recipient's own inbox. What a delivered message does (land in a subagent's
inbox, wake the main loop) is the recipient's business, kept out of this
package so the broker only routes.

## Contract

```go
package broker

const Main = "main"

type Message struct {
	From    string // sender address
	To      string // recipient address
	Subject string // short human-facing notice (may be empty)
	Content string // body delivered to the recipient
}

func Register(addr string, deliver func(Message)) // add a recipient
func Unregister(addr string)                      // remove it (idempotent)
func Send(m Message) bool                          // routes to m.To; false when no one holds it
func Reset()                                       // test-only
```

Addresses are unique per run — a task id (`generateShortID()`) or `Main` — so
an address is held by exactly one agent at a time; `Register`/`Unregister` are
a plain add/remove on the map. The registry is a process-wide singleton, like
`task.Default()`. The delivery function runs outside the lock and must not
block (it enqueues into the recipient's inbox and returns).

## Who uses it

| Sender | Sends |
|---|---|
| task lifecycle (`app`) | a background task's completion to `Main` |
| `SendMessage` tool | main → a running subagent, or a subagent → `Main` |

| Recipient | Registers |
|---|---|
| main loop (`app`) | `Main` → forwards onto the main-loop notice channel |
| each background subagent (`subagent.Executor`) | its task id → pushes to its `core.Agent` inbox |

A background task's **completion** is pushed automatically when its run ends.
Main injects it immediately while idle, or at the next turn boundary during an
active stream; it never polls `TaskOutput`. A **`SendMessage`** is best-effort:
a subagent that has finished (or never
takes another step) won't see it, so it is only for steering or interim notes
— a subagent's final result comes back on its own (the tool result for a
foreground run, the completion for a background one), never via `SendMessage`.

## See Also

- Subagents: [`subagent`](subagent.md)
- Background tasks: [`task`](task.md)
- Layer: `feature`
