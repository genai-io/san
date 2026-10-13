---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Owns durable session identity and application state. `internal/agent.Runtime`
owns the running agent; `internal/transcript` owns the append-only event log.

## Entrypoints and core types

- `Active` (`active.go`) binds the current ID, `Store`, and main `Writer`.
  `SetID` retires the previous writer. `Close` releases it at shutdown.
- `Snapshot` (`snapshot.go`) holds `Metadata`, native `[]core.Message`
  (`ai.Message`), todo items, notices, and tool presentation details. Messages remain native;
  notices and display details are separate from model input.
- `Store` (`store.go`) provides save, load, list, fork, and delete. `Save`
  imports a complete conversation; `SaveState` saves product state when the
  live writer already owns message persistence. `List` returns `[]Summary`.
- `Writer` (`writer.go`) consumes agent events and calls `transcript.Store.Append`.
  It records native messages without an intermediate content DTO. `Err` exposes
  the first persistence failure and stops subsequent conversation writes.
- `initialize.go` wires the process default. `todo.go` adapts todo snapshots;
  `title.go` and `title_text.go` derive display titles without changing history.

## Flow

```text
user / model → ai.Message → Writer → transcript.Store.Append → JSONL
JSONL → transcript.Project → Snapshot.Messages → agent.Runtime.Start
                                            → ChatRows → conversation UI
```

`ChatRows` is a display projection. Restored native messages seed the agent
without converting the rendered rows back into provider messages. Thinking
signatures, encrypted reasoning, tool-call signatures, tool-result images, and
content order survive unchanged. Live turn saves contain product state only.
A snapshot import uses `conversation.replaced`; repeated identical imports or
resume announcements produce no duplicate history.

## Storage and configuration

```text
~/.san/projects/<24-hex-workdir-hash>/
  transcripts/<24-hex-session-id>.jsonl
  transcripts/<session-id>.jsonl.meta
  blobs/tool-result/<session-id>/<tool-call-id>
  blobs/image/<session-id>/...
```

The project key hashes the absolute working directory; the session ID is random.
Parent relationships live in explicit `parentSessionId` fields. Paths do not
encode ancestry. `LoadHistory` displays the selected branch across compaction boundaries;
resume still uses only the active context. Notices retain their positions and
tool diffs come from separate `tool.completed` events.

Schema version `2` appears on `session.started`; messages and
schemas use SDK-native JSON. Older formats and directories are unsupported.

Each transcript has its own derived summary, validated against log size and
modification time. Listing discovers log files directly, so one process cannot
overwrite another session's shared index. Missing or corrupt summaries rebuild
from the log. Transcript records are authoritative.

## Lifecycle and failure handling

- Resume seeds message position and inference/exchange counters from the log.
  Model, provider, output budget, and attempt belong to each inference.
- A session permits one live writer, enforced by a file lock across processes.
  Runtime rebuilds reuse the main writer; child runtimes use independent IDs.
- `/clear` stops the runtime, retires the writer, adopts a fresh identity,
  updates hook input and task storage, and clears conversation state.
- Fork stops the runtime before copying history and attachments. It preserves
  historical timestamps, rewrites tool-output references to the child's blobs,
  and records the source ID on the child's fork event.
- Reads ignore an incomplete final JSON fragment. The next append repairs that
  fragment first; corruption in a terminated record remains an error.
- A failed append never advances the writer's parent pointer. The app surfaces
  persistence errors instead of acknowledging a successful save.

## Tests and pitfalls

`message_test.go` covers native roundtrips, failed writes, actual inference
configuration, resume deduplication, and independent fork output.
`writer_*_test.go` covers lifecycle ordering and compaction. Transcript recovery,
writer lease, and summary tests live under `internal/transcript`.
`history_test.go` and `notice_test.go` preserve mainline history and notice behavior.
`internal/app/model_session_test.go` covers the clear boundary; integration tests
live in `tests/integration/session`.

Never use UI rows as the live persistence source. Close a writer before creating
another writer for the same ID. Summary files may be deleted and rebuilt; the
JSONL and its blobs are the durable session.

## See also

- [Agent runtime](agent.md)
- [Inspector](inspector.md)
- [Package map](../../reference/package-map.md)
