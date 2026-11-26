---
package: github.com/genai-io/san/internal/agent/reminder
layer: feature
---

# Reminders

Hosts use `reminder.NewQueue()` to create a `*reminder.Queue` for a conversation. The queue attaches changing context to user messages as `<system-reminder>` blocks, leaving the system-prompt cache prefix unchanged. See [Context management](../reference/context-management.md).

## Queue and input lifecycle

- [`internal/agent/reminder/queue.go`](../../internal/agent/reminder/queue.go) owns provider registration, pending entries and deduplication. Providers register a source ID and a render function. `RequeueSystemReminders` replaces pending snapshots from the same provider; an empty render emits nothing unless previously delivered context needs to be withdrawn. One-time hook or command notices are queued independently.
- [`internal/agent/reminder/input.go`](../../internal/agent/reminder/input.go) owns conversation preparation, reset and restore. `PrepareInput` renders initial provider context, appends pending blocks and drains the queue. `Prepared()` means prepared, not delivered: the input queue retains the prepared message if delivery is rejected.
- `/clear` resets preparation. After resume or automatic compaction, active context seeds deduplication so the next input sends only missing or changed provider snapshots. See [Compaction and resume](../reference/context-management.md#compaction-and-resume).

Provider context includes the skills directory, user and project instructions, and selected memory recall. `memory-auto` remains a source ID for older sessions; current `MEMORY.md` previews use [Auto Memory Asides](../concepts/auto-memory.md#index-preview).

## Shared message format

[`internal/agent/reminder/block.go`](../../internal/agent/reminder/block.go) defines how to wrap, recognize, parse and append reminder blocks, including optional source attribution. Consumers can use these helpers without creating a queue.
