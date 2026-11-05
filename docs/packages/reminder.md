---
package: github.com/genai-io/san/internal/reminder
layer: feature
---

# reminder

Manages `<system-reminder>` content the harness attaches to user
messages. Reminders are cache-friendly: they live in immutable
conversation history once attached and re-inject once per conversation
(new session, `/clear`, compaction) without invalidating the system-prompt cache prefix.

## Purpose

The system prompt is for things true of *every* San session
(identity, policy). Reminders are for things that change *during* a
session and need to surface mid-conversation without busting the prompt
cache — currently:

- skills directory (re-emit when a skill is enabled / disabled / activated)
- memory user / memory project (AGENTS.md; re-emit on memory file change)
- memory auto (both auto-memory stores' `MEMORY.md` indexes, read at render time)
- memory recall (the auto memories picked for one message; no provider, attached to a tool result by `agent.WithRecall`)
- one-time notices (queued from hooks or commands)

See [`concepts/harness-channels.md`](../concepts/harness-channels.md) for
why reminders are a separate channel from the system prompt.

## Contract

The harness holds a concrete `*Reminder`. Sources register an ID and render
closure; returning an empty body skips emission.

```go
package reminder

// Reminder holds registered providers, pending reminders and conversation state.
type Reminder struct { /* unexported */ }

func New() *Reminder
func (s *Reminder) Register(id string, render func() string)
func (s *Reminder) Unregister(id string)
func (s *Reminder) Enqueue(body string)
func (s *Reminder) RequeueSystemReminders()
func (s *Reminder) Prepared() bool
func (s *Reminder) PrepareInput(text string) string
func (s *Reminder) ResetConversation()
func (s *Reminder) RestoreHistory(messages []core.ChatMessage)
// ... and drain/peek accessors for the harness

// Standard provider IDs:
const (
    ProviderSkillsDirectory = "skills-directory"
    ProviderMemoryUser      = "memory-user"
    ProviderMemoryProject   = "memory-project"
    ProviderMemoryAuto      = "memory-auto"
    ProviderMemoryRecall    = "memory-recall"
)

// Wrap a body in the <system-reminder> tag with optional source attribution.
func Wrap(body string) string
```

## Internals

- `Reminder` holds `providers []provider`, a `pending []pendingEntry`
  queue and `prepared`. Mutex-protected, with no singleton or interface layer.
- `RequeueSystemReminders` is idempotent across repeated calls — it drops
  prior pending entries from the same provider before re-emitting, so
  first message → `PostCompact` → `/skills` toggle in close succession
  produces one emission per provider rather than three.
- One-time notices (`Enqueue`) persist independently of provider re-emits.
- `Wrap` adds the `<system-reminder source="...">…</system-reminder>`
  XML-style tag with optional source attribution.

## Lifecycle

- Construction: `app.newServices` creates the `*Reminder` with `reminder.New()`.
- Per-message: `PrepareInput` prepares initial provider context once, sets
  `prepared`, attaches pending reminders and drains the reminder queue.
  `Prepared()` describes preparation, not successful delivery. The input
  queue retains prepared text when delivery is rejected.
- Per-event: skill toggles / memory updates call `RequeueSystemReminders`
  to refresh provider-emitted reminders.
- Once per conversation: `ResetConversation` clears `prepared` and
  discards obsolete notices after `/clear` or compaction. `RestoreHistory`
  recognizes sourced provider markers in restored user messages on resume.
  `/clear` and `/resume` also discard notices belonging to the old conversation.
- On compaction: reminder blocks are **skipped, not summarized**.
  `core.BuildCompactionText` peels trailing `<system-reminder>…</system-reminder>`
  blocks from user content before the summarizer runs, then the harness calls
  `ResetConversation` so fresh provider
  state reattaches to the next user turn. All providers (skills, memory-user,
  memory-project, memory-auto) and one-time notices share this lifecycle. On PostCompact
  the harness re-reads memory from disk before re-emitting, so an edited
  memory file re-injects its latest content. See
  [`concepts/compaction.md`](../concepts/compaction.md).

## Tests

```
internal/reminder/conversation_test.go — preparation, reset/restore, rejected-input retry.
internal/reminder/reminder_test.go    — provider re-emission, notice
                                         queue, idempotence, wrap format.
```

## See Also

- Code: `internal/reminder/`
- Concepts: [`concepts/harness-channels.md`](../concepts/harness-channels.md), [`concepts/extension-model.md`](../concepts/extension-model.md)
- Memory commands: [`reference/slash-commands.md`](../reference/slash-commands.md) (`/memory`)
- Layer: `feature`
