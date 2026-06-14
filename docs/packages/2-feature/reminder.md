---
package: github.com/genai-io/san/internal/reminder
layer: feature
---

# reminder

Manages `<system-reminder>` content the harness attaches to user
messages. Reminders are cache-friendly: they live in immutable
conversation history once attached and re-inject on `SessionStart` /
`PostCompact` without invalidating the system-prompt cache prefix.

## Purpose

The system prompt is for things true of *every* San session
(identity, policy). Reminders are for things that change *during* a
session and need to surface mid-conversation without busting the prompt
cache — currently:

- skills directory (re-emit when a skill is enabled / disabled / activated)
- memory user / memory project (re-emit on memory file change)
- one-time notices (queued from hooks or commands)

See [`concepts/harness-channels.md`](../../concepts/harness-channels.md) for
why reminders are a separate channel from the system prompt.

## Contract

This package has two contracts — one for sources (`Provider` interface)
and one for the harness (concrete `*Service` struct).

```go
package reminder

// Provider supplies a reminder body on demand. Returning ""
// skips emission.
type Provider interface {
    ID() string
    Render() string
}

func NewProvider(id string, render func() string) Provider

// Service holds the runtime state: registered providers + the
// pending-reminders queue for the next user message. Concrete struct,
// no interface.
type Service struct { /* unexported */ }

func NewService() *Service
func (s *Service) Register(p Provider)
func (s *Service) Unregister(id string)
func (s *Service) Enqueue(body string)
func (s *Service) RequeueSystemReminders()
// ... and a few drain/peek accessors for the harness

// Standard provider IDs:
const (
    ProviderSkillsDirectory = "skills-directory"
    ProviderMemoryUser      = "memory-user"
    ProviderMemoryProject   = "memory-project"
)

// Wrap a body in the <system-reminder> tag with optional source attribution.
func Wrap(body string) string
```

### Known Violations

This package is the **best example** of the pattern this codebase should
move toward:

- `Provider` is a 2-method interface (`ID`, `Render`) — perfect Go style.
- `Service` is a **concrete struct**, not a `Service` interface.
- No singleton: callers (`internal/app`) hold a `*Service` directly.
- `NewProvider` is a small helper for "just give me a closure-backed
  provider" — no need to declare a new type per source.

A handful of nits:

- **`NewProvider` returns the interface `Provider`.** Returning a
  concrete `providerFunc` type would be more idiomatic, but
  `providerFunc` is unexported, so the interface return is forced.
  Consider exporting it.

## Internals

- `Service` holds `providers []Provider` + a `pending []pendingEntry`
  queue. Mutex-protected.
- `RequeueSystemReminders` is idempotent across repeated calls — it drops
  prior pending entries from the same provider before re-emitting, so
  `SessionStart` → `PostCompact` → `/skills` toggle in close succession
  produces one emission per provider rather than three.
- One-time notices (`Enqueue`) persist independently of provider re-emits.
- `Wrap` adds the `<system-reminder source="...">…</system-reminder>`
  XML-style tag with optional source attribution.

## Lifecycle

- Construction: `app.Initialize` creates the `*Service` and passes it
  to the harness for the conversation builder.
- Per-message: when the user submits, the harness calls
  `DrainPending()` and wraps the bodies into the outgoing user message.
- Per-event: skill toggles / memory updates call `RequeueSystemReminders`
  to refresh provider-emitted reminders.
- On compaction: reminder blocks are **skipped, not summarized**.
  `core.BuildCompactionText` peels trailing `<system-reminder>…</system-reminder>`
  blocks from user content before the summarizer runs, then the harness calls
  `DiscardPendingNotices` + `RequeueSystemReminders` so fresh provider state
  reattaches to the next user turn. All providers (skills, memory-user,
  memory-project) and one-time notices share this lifecycle. On PostCompact
  the harness re-reads memory from disk before re-emitting, so an edited
  memory file re-injects its latest content. See
  [`concepts/compaction.md`](../../concepts/compaction.md).

## Tests

```
internal/reminder/reminder_test.go    — provider re-emission, notice
                                         queue, idempotence, wrap format.
```

## See Also

- Code: `internal/reminder/`
- Concepts: [`concepts/harness-channels.md`](../../concepts/harness-channels.md), [`concepts/extension-model.md`](../../concepts/extension-model.md)
- Memory commands: [`reference/slash-commands.md`](../../reference/slash-commands.md) (`/memory`)
- Layer: `feature`
