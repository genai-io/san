# Context Management

San keeps the start of each request unchanged when it can, so the provider can reuse cached input. When something changes, San sends the new state instead of preserving a stale prompt for the sake of a cache hit.

## Principles

- **Keep the prefix predictable.** The same inputs should produce the same text and order. Don't reorder tools or rewrite old messages on a no-op reload.
- **Send details when needed.** `AGENTS.md` should contain rules and pointers, not a directory listing. A skill description points to its body; `MEMORY.md` points to topic notes. Empty indexes are omitted, and each nonempty index gets a short preview.
- **Keep permissions live.** A schema tells the model what it can ask for, not what it may execute. Check permissions at execution time.

## Request layout

| Request field | What goes in it |
|---|---|
| `tools` | Schemas for the tools San advertises, including enabled MCP tools. |
| `system` | **Agent instructions** first: identity, behavior, and San's rules. Then the **runtime environment**: working directory, date, branch, and memory store paths. A Persona can add guidance but cannot remove San's base rules. |
| `messages` | User input, assistant replies, tool results, and reminders such as project instructions or skill descriptions. |

A working directory change updates the environment without rewriting the agent instructions. That doesn't yet create a separate cache entry: sdk-go serializes the Anthropic system prompt as one block. Anthropic caches prefixes in `tools → system → messages` order. Changing a tool schema can invalidate the rest of the prefix; changing the environment can invalidate the system block. A cache breakpoint after the agent instructions needs SDK support. Use `cache_read` to verify reuse.

## Reminders and hot reload

The first user message carries applicable project instructions and short indexes. San reads user instructions, then the `AGENTS.md` chain from the repository root to the working directory, plus rules and `AGENTS.local.md`, under a shared size cap. Full skill bodies and memory notes are loaded when needed.

On reload, unchanged reminder text isn't appended again. A new value supersedes the old one; a removal is explicit. Changes apply at the next inference boundary supported by the host, not to a request already in flight. The `<system-reminder>` tag formats the text; it does not confer authority.

## Compaction and resume

```text
summarize dialogue → commit context.compact → replace active messages → continue
```

Auto-compact can run near the [context limit](token-limits.md) or after a provider rejects an oversized request. It sends the summary and fresh reminders as separate blocks before continuing. With manual `/compact`, the next user input continues from the summary.

The full transcript stays intact. Resume loads only the active messages, adds any missing or changed reminders, and doesn't rerun old tools. The TUI reports changes to recorded agent instructions, not environment-only changes.

## Implementation and checks

| Where | Responsibility |
|---|---|
| `internal/core/system` and `internal/core/run.go` | Assemble and refresh the system prompt. |
| `internal/app/agent.go` and `internal/reminder` | Register sources and emit reminder updates. |
| `internal/core/compact.go` and `internal/agent/compaction.go` | Trigger compaction and restore context. |
| `internal/session` | Record requests and reconstruct active messages. |

Use the [Inspector](../guides/inspector.md) to check that no-op reloads preserve the prefix, published changes reach the next request, and compact or resume doesn't duplicate unchanged reminders.
