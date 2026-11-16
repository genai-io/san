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
  until that same owner registers again or signals recovered inbox capacity with
  **RetryCompletions**. It never redirects to main or starts an agent.

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

func Register(addr string, deliver func(Message) bool) func() // register; return scoped cleanup
func RegisterAccept(addr string, accept func(Message) error) func() // same, with rejection reasons
func Unregister(addr string)                        // remove the current recipient
func Send(m Message) bool                           // false when absent or rejected
func SendErr(m Message) error                       // ErrNoRecipient, ErrRejected or recipient reason
func SendCompletion(m Message) bool                 // delivered or retained for its child owner
func RetryCompletions(addr string)                  // capacity wake for retained completion mail only
func Reset()                                        // test-only
```

Addresses identify logical agents — a task id (`generateShortID()`) or `Main` — so
an address is held by exactly one registration at a time. Use the cleanup
returned by `Register` for execution-owned routes: it is idempotent and cannot
remove a later replacement. `Unregister` removes whichever route is current.
The registry is a process-wide singleton, like `task.Default()`. The delivery
function runs outside the global broker lock and may synchronously persist
admission before returning. Acceptance means admitted, not processed. Receivers
must coordinate shutdown with in-flight admission: preserve accepted mail and
reject admission after closure. Unregistering does not cancel an already-running
callback. `RegisterAccept` recipients
may return `ErrInboxFull` (or another reason) so `SendErr` callers can report
the actual cause; a plain `Register` rejection surfaces as `ErrRejected`.
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

## Retained completion capacity wakes

The receiver calls `RetryCompletions(addr)` after inbox capacity recovers, using
its existing task-ID address. No re-registration, polling, actor startup, fallback,
or ordinary-mail retry is involved. An absent owner remains absent and keeps its
retained mail until a later registration. There is no additional configuration.

A single drainer per owner preserves FIFO and pending deduplication (including
the in-flight head). Capacity wakes during an admission coalesce into a retry
if that admission rejects, preventing a lost wake when capacity recovers before
a stale full-inbox rejection returns. Each admission consumes earlier wakes;
a rejection without a new wake or replacement registration stops the drain.
Callbacks can reenter broker APIs without the global lock being held. A receiver
should signal actual capacity changes, not unconditionally signal on each
rejection, which would continually request retries. Retry may synchronously run
admission callbacks. Scoped registration cleanup and Reset cannot resurrect a
detached queue.

Native coverage lives in `internal/broker/completion_test.go` and
`internal/broker/retry_test.go`: capacity recovery, in-flight rejection wakes,
FIFO/deduplication, reentrancy, registration replacement, cleanup, Reset, and
absence of fallback/startup/ordinary-delivery effects. Run
`go test -race ./internal/broker`.

## See Also

- Subagents: [`subagent`](subagent.md)
- Background tasks: [`task`](task.md)
- Layer: `feature`
