---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Owns durable conversation identity and the opened Session that writes its transcript.
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
  `Append` accepts a typed transcript record. `RecordEvent` adapts native
  agent events to the same append implementation; application state and audit
  producers use `Append` directly. `Err` reports a failed or closed handle.
  `Close` waits for in-flight appends and releases the lease exactly once.
- **Runtime** runs the agent and owns its authoritative native message chain.
  `Start` runs it, `Stop` cancels execution while retaining messages, and `Reset`
  clears the conversation after stopping. Runtime never owns transcript files
  or writer leases. Several runtime generations can use the same opened Session.
- **History** is the selected native conversation and application/display data
  read from a transcript: metadata, messages, todo items, notices, and tool
  presentation details. This read result supplies Runtime and the UI during
  restoration. Session owns the resources and writes; History owns only data.
- **Transcript** is the append-only log of what happened. It is authoritative.
  Its `Record` payloads keep SDK-native messages and schemas, and native todo
  items. `transcript.Store` implements log storage, replay, recovery, and derived
  summaries beneath the product-facing Store and Session.

All writes share one append implementation, with typed adaptation at the SDK
and application boundaries.

## Entrypoints and core packages

- `store.go`: `NewStore`, `Create`, `Open`, `Read`, `ReadChatHistory`, `List`,
  `Fork`, `Delete`, `RecordSubagentConversation`, and attachment storage.
- `session.go`: the opened `Session`, `CreateOptions`, `Append`, `Err`, `Close`.
- `events.go`: `RecordEvent`, inference spans, compaction and native event
  adaptation. Internal method names describe the event being persisted.
- `history.go`: `History` and its one-way `ChatRows` display projection.
- `internal/app/model_session.go`: current selection, application state records,
  restore, clear, and fork. `services` holds Store and the opened Session directly.
- `internal/agent/runtime.go`: live execution and native message retention.

The application creates Store at startup and injects it into slash commands.
`/resume` lists that Store and opens the session selector. Selecting a session
asks the application to stop Runtime and restore the selected identity.

## Flow

```text
SDK native event -> Session.RecordEvent -> shared append -> transcript
application change -> Session.Append --------------------------> transcript

Store.Open(id) -> claim writer lease -> read/project once -> Session + History
                                                           |          |
                                                           |          +-> native messages -> Runtime
                                                           |          +-> ChatRows -> dialogue UI
                                                           +-> later appends

switch -> Runtime.Stop -> open replacement -> retire previous Session/UI context
       -> restore history and bind identity/hooks/group/tasks
clear  -> Runtime.Reset -> Session.Close -> select fresh identity
quit   -> Runtime.Stop -> Session.Close
```

Application state writes contain title, prompt preview, mode, autopilot, group,
and tasks. `StatePatch` has typed optional fields: nil leaves a field unchanged;
an explicit empty value clears it. Unused tag/worktree fields and generic JSON
patch operations are removed. The transcript computes their actual differences atomically and skips
unchanged fields. State records contain application data; native conversation
records contain messages. State appends happen in order on the application loop.
Git branch annotations are recorded with inference requests on the agent goroutine,
keeping Git subprocesses off the UI loop. Appends sync durable records before
returning. Listing refreshes derived summaries lazily.
Unreadable or unsupported transcripts are logged and skipped during listing;
opening one explicitly still reports its error. Other sessions remain available.

Native messages seed Runtime directly. UI rows never become the persistence
source. Thinking signatures, encrypted reasoning, tool signatures, images, and
content ordering survive. Request-time capability filtering does not mutate the
native history. Runtime retains it through stops and errors. Inputs rejected before
agent acceptance remain in a native pending queue; rebuilding never reads UI rows.
Accepted input is announced and retained even when inference fails or a run stops
before its next step. Runtime rejects sends once its mailbox starts stopping.
`Store.ChatRows` expands persisted tool output only for display. Runtime receives
the original preview and attachment reference, keeping resumed context bounded.

Completed subagents write native conversation replacement and title records
through their own opened Session.

## Storage and configuration

```text
~/.san/projects/<24-hex-workdir-hash>/
  transcripts/<session-id>.jsonl
  transcripts/<session-id>.jsonl.meta
  blobs/tool-result/<session-id>/<tool-call-id>
  blobs/image/<session-id>/...
```

Paths do not encode ancestry. Session parents and message parents are distinct
fields. Transcripts use schema version `3` and SDK-native payloads. Metadata files
are disposable derived summaries. Earlier schema versions are rejected; no migration is provided.

## Lifecycle and failure handling

- IDs must be single path components; deletion claims the writer lease and rejects
  active sessions. Resume and clear reset session-specific autopilot state.
- A writer lease is held from Open/Create until Close, including idle resume.
  Every new lease refreshes cached state after another process's writes.
- Failed opens release their candidate lease and do not redirect the selected
  session. A retired runtime's callback remains bound to its retired Session.
- A successful identity switch cancels the previous session's displayed and
  queued interactions, rejects requests from its retired UI owner, and discards
  pending one-time context. First open, same-ID resume, and failed opens retain
  the current UI owner and pending context.
- Clear, fork, and resume to a different identity discard temporary permission
  grants and reapply the selected permission mode. Same-ID and failed resume
  preserve those grants. Retired outbox events cannot change the selected UI;
  a naturally finished run keeps its outbox long enough to drain final events.
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
`RecordEvent` adaptation, `Err`, and `Close`; its Store contract supplies a
writer claim before restore. SDK `session.Read` is a query without an opened
writer. SDK `session.Create` creates a new identity and `session.Open` restores an existing
one. SDK returns native `[]ai.Message`; it does not know San's titles, tasks,
groups, or UI projections. These remain application records in San.

`EntryConversationReplaced` records a complete native conversation in `Messages`.
Storage implementations share the same native message structs.

## Tests and pitfalls

Session/native/event tests cover message roundtrips, write failure, request
configuration, compaction, notice position, lease handoff, and fork ownership.
`internal/app/session_resume_test.go` covers parallel tool results across retry,
compaction before runtime startup, image-preserving model switches, live renaming,
group identity changes, and UI/context isolation across session switches.
Transcript tests cover recovery and atomic state patching; integration fixtures
seed records through the public Append contract.
`internal/app/session_isolation_test.go` covers temporary grants, worker notices,
and stale outboxes. Runtime pending-input tests cover failure and early shutdown;
`internal/session/list_recovery_test.go` covers malformed and old-version neighbors.

Run `make ci`. Always stop Runtime before closing its Session. ReadChatHistory is for
browsing; Open restores the active context. Never write UI rows or a read result
back at the end of a turn.

## See also

- [Agent runtime](agent.md)
- [Native transcript log](transcript.md)
- [Inspector](inspector.md)
- [Package map](../../reference/package-map.md)
