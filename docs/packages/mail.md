---
package: github.com/genai-io/san/internal/agent/mail
layer: feature
---

# mail

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
  until that same owner registers again or reports free inbox room with
  **WakeCompletions**. It never redirects to main or starts an agent.

Direct addressing only; ordinary `Send` remains unqueued.

## Contract

```go
package mail

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
func WakeCompletions(addr string)                   // inbox has room: retry retained completion mail only
func Reset()                                        // test-only
```

Addresses identify logical agents — a task id (`generateShortID()`) or `Main` — so
an address is held by exactly one registration at a time. Use the cleanup
returned by `Register` for execution-owned routes: it is idempotent and cannot
remove a later replacement. `Unregister` removes whichever route is current.
The registry is a process-wide singleton, like `task.Default()`.

The delivery function runs outside the global broker lock and may persist the
message before returning; acceptance means admitted, not processed. Because
`Register`, `SendCompletion` and `WakeCompletions` can run delivery
synchronously, never call them while holding a lock that delivery needs (for
example the task lock). Receivers coordinate shutdown with in-flight admission:
they preserve accepted mail and reject it after closure. Unregistering does not
cancel an already-running callback. `RegisterAccept` recipients may return
`ErrInboxFull` (or another reason) so `SendErr` callers can report the actual
cause; a plain `Register` rejection surfaces as `ErrRejected`.
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

## Waking retained completions

A receiver whose inbox was full calls `WakeCompletions(addr)` when it has room
again, using its existing task-ID address.

- It retries only retained completion mail against the live registration. It
  never registers, polls, starts an agent, falls back to `Main` or redelivers
  ordinary mail. An absent owner keeps its mail until it registers.
- One drainer per owner keeps FIFO order and pending deduplication, including
  the in-flight head.
- A wake that arrives while a delivery is in flight is recorded. If that
  delivery is then rejected (a stale "inbox full"), the drainer retries once
  more instead of losing the wake. Otherwise a rejection stops the drain until
  the next wake or registration.
- Signal real room changes, not every rejection, or the drain keeps retrying.
- Scoped registration cleanup and `Reset` cannot resurrect a detached queue.

## Tests

- `internal/agent/mail/completion_test.go` — retention, ordering, deduplication,
  replacement and cleanup.
- `internal/agent/mail/wake_test.go` — room recovery, wakes during an in-flight
  rejection, reentrancy, `Reset`, and no fallback, startup or ordinary delivery.

## See Also

- Subagents: [`subagent`](subagent.md)
- Background tasks: [`task`](task.md)
- Layer: `feature`
