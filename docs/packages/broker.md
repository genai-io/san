---
package: github.com/genai-io/san/internal/broker
layer: feature
---

# broker

Routes messages to active agent inboxes. Session creation and continuation belong
to the executor and tools.

## Model

- An agent **Registers** an address to receive at when it starts (the main
  conversation under `Main`, a subagent execution under its stable task id) and
  **Unregisters** when it stops.
- Anyone **Sends** a `Message` stamped with a destination (`To`). The broker
  delivers it to whoever holds that address; a message to an address no one
  holds returns `false`.

- **SendCompletion** retains undelivered child-owned task results in process memory
  until that same owner registers again. It never redirects to main or starts an agent.

Direct addressing only; ordinary `Send` remains unqueued.

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
func SendCompletion(m Message) bool                // delivered or retained for its child owner
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
| task lifecycle (`app`) | a completion to its launching agent; unowned work goes to `Main` |
| `SendMessage` tool | main → a running subagent, or a subagent → `Main` |

| Recipient | Registers |
|---|---|
| main loop (`app`) | `Main` → forwards onto the main-loop notice channel |
| each active subagent execution (`subagent.Executor`) | its task id → `core.Agent.Append`, queued for the next step |

Child-owned Bash completions enter only the child's context, not main's chat or
tracker. Foreground results return through the tool call.

`SendMessage` uses the broker for running workers. For idle workers, the tool
checks permissions and asks the executor to continue the existing session.
See [task lifecycle and continuation](task.md#continuation-and-failure-handling).

Acceptance means input was queued, not that work is complete. The executor
preserves accepted input in history if execution fails or is canceled.

## See Also

- Subagents: [`subagent`](subagent.md)
- Background tasks: [`task`](task.md)
- Layer: `feature`
