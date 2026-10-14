---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Owns durable conversation identity and the opened handle that writes its transcript.
The application owns which session is selected. `agent.Runtime` owns execution.

## Responsibilities and names

- **Store** manages multiple sessions in one project: create, open, read, list,
  fork, delete, and store attachments. It does not run an agent or own the current
  selection. `Create` creates a new identity; `Open` acquires an existing
  identity's writer lease before restoring it. `Read` reads without opening a
  writer, for queries. `ReadChatHistory` includes the selected pre-compaction branch
  for display. `Fork` returns an opened child and its restored history.
- **Session** is one opened identity, fixed until `Close`. It owns the writer
  lease, ordered appends, inference/exchange positions, and persistence failures.
  `Append` accepts a typed transcript record. `RecordAgentEvent` adapts native
  agent events to the same append implementation; application state and audit
  producers use `Append` directly. `Err` reports a failed or closed handle.
  `Close` waits for in-flight appends and releases the lease exactly once.
- **Runtime** runs the agent and owns its authoritative native message chain.
  `Start` runs it, `Stop` cancels execution while retaining messages, and `Reset`
  clears the conversation after stopping. Runtime never owns transcript files
  or writer leases. Several runtime generations can use the same opened Session.
- **History** is the selected native conversation and application/display data
  read from a transcript: metadata, messages, todo items, notices, and tool
  presentation details. It is a return value, not an independently managed
  object. It holds no locks, agent, save method, or lifecycle. Grouping the read
  result keeps restoration to one pass without parallel return values drifting.
  Daily persistence never builds or saves History.
- **Transcript** is the append-only log of what happened. It is authoritative.
  Its `Record` payloads keep SDK-native messages and schemas, and native todo
  items. `transcript.Store` implements log storage, replay, recovery, and derived
  summaries beneath the product-facing Store and Session.

`Active`, `Writer`, `Snapshot`, `Handle`, `Save`, `SaveState`, callback factories,
mirrored todo DTOs, and compatibility aliases are absent. There is one append
implementation, with typed adaptation at the SDK and application boundaries.

## Entrypoints and core packages

- `store.go`: `NewStore`, `Create`, `Open`, `Read`, `ReadChatHistory`, `List`, `Latest`,
  `Fork`, `Delete`, `RecordSubagentConversation`, and attachment storage.
- `session.go`: the opened `Session`, `CreateOptions`, `Append`, `Err`, `Close`.
- `events.go`: `RecordAgentEvent`, inference spans, compaction and native event
  adaptation. Internal method names describe the event being persisted.
- `history.go`: `History` and its one-way `ChatRows` display projection.
- `internal/app/model_session.go`: current selection, application state records,
  restore, clear, and fork. `services` holds Store and the opened Session directly;
  there is no intermediate session manager.
- `internal/agent/runtime.go`: live execution and native message retention.

## Flow

```text
SDK native event -> Session.RecordAgentEvent -> shared append -> transcript
application change -> Session.Append --------------------------> transcript

Store.Open(id) -> claim writer lease -> read/project once -> Session + History
                                                           |          |
                                                           |          +-> native messages -> Runtime
                                                           |          +-> ChatRows -> dialogue UI
                                                           +-> later appends

switch/quit -> Runtime.Stop -> Session.Close
switch      -> open replacement -> publish identity/hooks/group/task bindings
```

Application state writes contain title, prompt preview, mode, autopilot, group,
and tasks. The transcript computes their actual differences atomically and skips
unchanged fields. They never carry the conversation. State appends happen in
order on the application loop, so a delayed save cannot overwrite a new session.
Git branch annotations are recorded with inference requests on the agent goroutine,
so saving application state starts no Git subprocess on the UI loop.
Summary refresh is lazy on listing; there is no extra Flush or save barrier.

Native messages seed Runtime directly. UI rows never become the persistence
source. Thinking signatures, encrypted reasoning, tool signatures, images, and
content ordering survive. Request-time capability filtering does not mutate the
native history. Runtime retains it through stops and errors.

Completed subagents write native conversation replacement and title records
through their own opened Session. They do not build a History object to save.

## Storage and configuration

```text
~/.san/projects/<24-hex-workdir-hash>/
  transcripts/<session-id>.jsonl
  transcripts/<session-id>.jsonl.meta
  blobs/tool-result/<session-id>/<tool-call-id>
  blobs/image/<session-id>/...
```

Paths do not encode ancestry. Session parents and message parents are distinct
fields. Schema version `2` and SDK-native payloads are required; old formats and
migration paths are unsupported. Metadata files are disposable derived summaries.

## Lifecycle and failure handling

- A writer lease is held from Open/Create until Close, including idle resume.
  Every new lease refreshes cached state after another process's writes.
- Failed opens release their candidate lease and do not redirect the selected
  session. A retired runtime's callback remains bound to its retired Session.
- Runtime rebuilds reuse the opened Session. Agent-start events establish a new
  exchange offset without clearing durable history.
- Clear resets Runtime, closes the old Session, releases group identity, and
  selects a new identity before accepting another input.
- Fork stops Runtime, copies history and assets, then opens/replays the child
  once. The source's membership remains; the child starts without group membership.
- A failed conversation/state append stops later writes. Parent position advances
  only after success. Audit errors are logged through the same append implementation.
- Incomplete final fragments are repaired before append; terminated corruption
  remains an error. Manual compaction before a resumed Runtime starts records
  both its native summary and boundary immediately.

## SDK Go responsibilities

SDK `session.Session` follows the same resource and naming rules: typed `Append`,
`RecordAgentEvent` adaptation, `Err`, and `Close`; its Store contract supplies a
writer claim before restore. SDK `session.Read` is a query without an opened
writer. SDK `session.Create` creates a new identity and `session.Open` restores an existing
one. SDK returns native `[]ai.Message`; it does not know San's titles, tasks,
groups, or UI projections. These remain application records in San.

A conversation replacement is named `EntryConversationReplaced`, with native
`Messages`, rather than a generic Snapshot. There is no Handle/Record compatibility
surface. Storage implementations may vary; message structs do not.

## Tests and pitfalls

Session/native/event tests cover message roundtrips, write failure, request
configuration, compaction, notice position, lease handoff, and fork ownership.
`internal/app/session_resume_test.go` covers parallel tool results across retry,
compaction before runtime startup, image-preserving model switches, live renaming,
and group identity changes. Transcript tests cover recovery and atomic state
patching; integration fixtures seed records through the public Append contract.

Run `make ci`. Always stop Runtime before closing its Session. ReadChatHistory is for
browsing; Open restores the active context. Never write UI rows or a read result
back at the end of a turn.

## See also

- [Agent runtime](agent.md)
- [Native transcript log](transcript.md)
- [Inspector](inspector.md)
- [Package map](../../reference/package-map.md)
