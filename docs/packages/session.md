---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Owns durable conversation identity and the opened Session that writes its transcript.
The application owns which session is selected. `agent.Runtime` owns execution.

Sessions persist as JSONL through sdk-go's file store. The
[session management design](https://github.com/genai-io/san/pull/736)
describes proposed changes separately from the current implementation.

## Responsibilities and names

- **Store** manages multiple sessions in one registered project:
  create, open, read, list, fork, delete, and store attachments. Git common-directory
  identity groups linked worktrees; non-Git projects use the exact startup path.
  Legacy cwd-hash partitions remain separate. Store does not run an agent or
  own the current selection. `Create` creates a new identity; `Open` acquires an existing
  identity's writer lease before restoring it. `Read` reads without opening a
  writer, returning native `History`. `Read(id, ReadOptions{})` selects active context;
  `ReadOptions{FullHistory: true}` includes pre-compaction messages on the selected
  branch. Display callers separately use `Store.ChatRows(history)` for projection
  and asset hydration. `Fork` returns an opened child and its restored history.
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

- `store.go`: `OpenStore` (`NewStore` compatibility wrapper), `Create`, `Open`, `Read`, `ReadOptions`, `List`,
  `Fork`, `Delete`, `RecordSubagentConversation`, and attachment storage.
- `project.go`: derives the project ID from where the project is, writes its description, validates workspaces.
- `session.go`: the opened `Session`, `CreateOptions`, `Append`, `Err`, `Close`.
- `events.go`: `RecordEvent`, inference spans, compaction and native event
  adaptation. Internal method names describe the event being persisted.
- `history.go`: `History` and its one-way `ChatRows` display projection.
- `internal/app/model_session.go`: current selection, application state records,
  restore, clear, and fork. `services` holds Store and the opened Session directly.
- `internal/agent/runtime.go`: live execution and native message retention.

The application creates Store at startup and injects it into slash commands.
`san -r` and `/resume` paint the selector before listing sessions on a background
command. `ListContext` cancels cold-cache recovery when the picker closes;
generation checks discard late results after reopening. Filtering and terminal
resizing remain available while loading. Selecting a session asks the application
to stop Runtime and restore the selected identity.

The picker renders cached titles and prompt previews only, including short
explicit titles. Rendering, filtering, and navigation never read transcripts.
Missing or stale `.meta` files rebuild through a streaming metadata projection:
bounded user text and message IDs preserve truncation and compaction semantics,
while audit payloads and tool output are discarded. Main-session listings stop
at a sidechain's header, keeping that partial result in memory only. A full
sidechain listing still reads and validates its body.

Selected-session recovery also projects records as they arrive and seeds the
writer counters in that pass, without retaining the entire audit log. Once the
terminal size is known, scrollback commits render each message once; retained-row
measurement supplies the frame geometry. Full history still replays into native
scrollback through the normal print queue.

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
and tasks. `transcript.StatePatch` has typed optional fields: nil leaves a field unchanged;
an explicit empty value clears it. Unused tag/worktree fields and generic JSON
patch operations are removed. The transcript computes their actual differences atomically and skips
unchanged fields. State records contain application data; native conversation
records contain messages. State appends happen in order on the application loop.
Git branch annotations are recorded with inference requests on the agent goroutine,
keeping Git subprocesses off the UI loop. Changed conversation saves sync before
returning; application records sync on a subsequent changed conversation save
or writer close. A no-op save does not flush them. Listing refreshes derived
summaries lazily.
Unreadable or unsupported transcripts are logged and skipped during listing;
opening one explicitly still reports its error. Other sessions remain available.

Native messages seed Runtime directly. UI rows never become the persistence
source. Thinking signatures, encrypted reasoning, tool signatures, images, and
content ordering survive. Request-time capability filtering does not mutate the
native history. Runtime retains it through stops and errors. Inputs rejected before
agent acceptance remain in a native pending queue; rebuilding never reads UI rows.
Accepted input is announced and retained even when inference fails or a run stops
before its next step. Runtime rejects sends once its mailbox starts stopping.
`Store.ChatRows` expands persisted tool output only for display and reconstructs
image markers in their native text/image order. It hides San's model-only source
path paragraph from the resumed UI without removing it from the native
conversation; the model can still use that path after compaction or resume.
Runtime receives
the original preview and attachment reference, keeping resumed context bounded.

Completed subagents write native conversation replacement and title records
through their own opened Session.

## Typed-event persistence slice (not a runtime format switch)

```text
existing Runtime -> Session.Append(transcript.Record) -> schema-4 JSONL (unchanged)

new typed Event batches -> ApplyEventBatch -> immutable Projection
                                  | reject: keep original projection
                                  ` accept: candidate only, no I/O or execution
ReplayEventBatches --------------------------^ (preserves batch boundaries)
```

- `event_model.go`: the 13 typed payload families (identified by `seq`, linked by `source`), strict codec and tri-state patches. See [PROP-0005](../design/proposal-0005-session-management.md).
- `event_validation.go`: `EventValidationContext`, `ForeignEventEvidence`, retained
  validation merging, validated event transitions and reference checks. `mergeValidation` preserves its inputs;
  `mergeReplayValidation` mutates only exclusively owned replay state.
- `event_projector.go`: `ApplyEventBatch`, `ReplayEventBatches` and immutable
  `Projection` accessors. Native `ai.Message`/`ai.Schema` remain intact; full message
  history stays separate from active context. `context.compact` keeps messages appended after
  `through` and validates native call/result pairing; `Unfinished` reports calls that never ran,
  allowed calls with unknown outcome and interrupted inferences. Accessors detach snapshots.
- `event_query.go`: `Store.Events`, bounded pages pinned to a checked prefix.
- `lifecycle.go`: `library`, the project's sessions as directories. Trash writes `deleted.json`, holds the
  writer claim across one rename into `trash/<sid>/` (refusing busy or pinned sessions); restore
  renames back and never overwrites; purge removes trashed sessions past 7 days via a hidden rename,
  and keeps any of unknown age.
- `overview.go`: `Overview`, a session's list row projected from events, cached in `cache/<sid>.json`
  while the events file's stamp matches. `blob.go` stores standalone request copies; `attrs.go`
  holds user intent (renamed title, archive, pin).

`event_storage.go` adds the private typed writer. Each session is one directory,
`sessions/<sid>/`, holding `events.jsonl` (an sdk-go store opened on that directory with
the fixed ID `events`, see `event_log.go`), plus `attrs.json` and `blobs/` when present.
`createEventSession` builds the session in a staging directory and publishes it with one
rename; `openEventSession` replays, then claims the writer. `readEvents` rebuilds private
state and `replayEvents` returns its projection and SDK tail; both accept an optional
inclusive `tailLimit`. Each committed batch holds one version-1 `san.events` record with
typed events and retained validation evidence, published only after a successful Commit.
Ordinary `Store.Create` / `Open`, `Session.Append(transcript.Record)`, runtime, hooks and
inspector remain schema 4 under `transcripts/`; there is no dual write and no public typed
writer or resume API yet.

The public read surface is bounded raw events, not rendered history or resumable
context:

```go
func (s *Store) Events(ctx context.Context, id string, query EventQuery) (EventPage, error)
```

`EventQuery` has `Limit` and opaque `Cursor`; `EventPage` has `Events`,
`NextCursor` and `ThroughSeq`. Limit defaults to 100, accepts 1–500, and rejects
other values; cursors are capped at 2048 bytes. The first page validates a prefix;
continuations bind project/session, the SDK tail identity and event sequence.
Later appends do not extend that prefix; a changed or missing prefix fails rather
than restarting. Each page replays the **entire pinned prefix** before returning a
detached slice: bounded output is not bounded I/O, replay work or memory. There
are no filters, lifecycle-generation binding, page index or cached projection.

Reads take no writer and perform no repair. Typed open validates complete product
facts before claiming a writer and allowing SDK unfinished-tail repair, then
rechecks a changed tail. Strict decoding, identity/ID conflicts, malformed evidence
and invalid projections fail; legacy records are refused, not converted. Legacy
open/read paths likewise reject SDK batch format before legacy repair (see
[transcript](transcript.md)).

Retained foreign provenance, parent ancestry and fork-prefix evidence are stored
with batches and merged against only the committed prefix and current batch;
contradictions fail. These are acceptance facts, not permission, current trust or
live foreign-file validation. Stable batch IDs retain their original expected tail
across reopen: the SDK judges identical retries and conflicting content before
current-tail-dependent validation. An ambiguous commit error can poison the handle;
close/reopen and retry the same batch, rather than assuming rollback. Creation is
not an idempotent retry entrypoint or crash-atomic: the initial existence check
refuses an existing destination, and a failed claim/initial commit may leave an
empty file requiring explicit handling. That check alone cannot distinguish a
concurrently created, then abandoned empty file from this attempt's new file;
closing that race requires a future SDK exclusive-create primitive.

Open and reads replay the log; live preparation may copy accumulated projection
and evidence. Large histories and many small batches therefore carry CPU,
allocation and memory costs even for small pages. Tests include
`event_model_test.go` (codec, validation, projection), `event_storage_test.go`,
`event_storage_safety_test.go` and `event_query_test.go`;
`event_projector_bench_test.go` covers replay/preparation costs.

## Storage and configuration

```text
~/.san/projects/<project-id>/
  project.json                         # description only; identity is derived
  transcripts/<session-id>.jsonl
  transcripts/<session-id>.jsonl.meta
  blobs/tool-result/<session-id>/<tool-call-id>
  blobs/image/<session-id>/...
```

Paths do not encode ancestry; a session's parent is `parentSessionId`.
Ordinary runtime transcripts are sdk-go `pkg/session` logs with schema version `4`
and SDK-native payloads; the private typed path uses committed `san.events` batches.
Metadata files
are disposable derived summaries. Earlier schema versions are rejected; no migration is provided.

`OpenStore(ctx, cwd)` derives the project ID from the project's kind and canonical key: the Git
common directory, or the exact startup directory outside Git. Git subdirectories and worktrees
share one project; clones and submodules do not; non-Git child directories are distinct. There is
no registry to lock or reconcile, and `project.json` (kind, key, name) only describes the project.
A repository that moves is a new project. Old `<hash(cwd)>` partitions are not read.

`CreateOptions.Workspace` defaults to the captured checkout/root. An explicit workspace
must belong to the same project; it does not change project ownership or grant trust.
New start records persist project/workspace identity; legacy missing fields stay unknown.
Project identity does not select settings. Settings load and project-scope writes
use the current checkout root (exact startup cwd outside Git), with normal user-global
and existing local-file precedence. Project `settings.json` files are not copied or
synchronized; missing project settings do not fall back to another checkout.
The existing main-checkout local override and grant-write compatibility remains.
A valid linked checkout needs its common
Git metadata, not an available original main checkout or main configuration files.
`NewStoreWithDir` remains an explicit-directory construction path for embedding/tests.
Close all Session handles before `Store.Close()`. `TranscriptPath` supplies the live
JSONL path used by hooks and inspection consumers. Trash and automatic retention remain
proposed; current Delete still removes files immediately.

## Lifecycle and failure handling

- Resume checks a known saved cwd against the requested/runtime directory using
  canonical paths (including symlinks). A mismatch or missing saved directory fails
  before replacing selection; reopen in the saved directory. Implicit directory
  switching is not implemented. Unknown legacy cwd remains readable. A rejected
  switch preserves the old lease and permissions, not a stopped runtime's execution.
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
- A failed conversation/state append stops later writes. Audit errors are logged
  through the same append implementation.
- An unfinished final line is ignored on read and cut by the next writer;
  terminated corruption remains an error. Manual compaction before a resumed Runtime starts records
  both its native summary and boundary immediately.

## SDK Go responsibilities

The pinned sdk-go `pkg/session` exposes `Open(dir)` to open a directory-backed
Store, `Store.Load(ctx, id)` to read native messages, and `Store.Writer(ctx, id)`
to acquire a writer. `Writer.Save(messages)` persists conversation changes;
`Writer.Append(name, data)` appends application records; `Writer.Close()` syncs
and releases ownership. SDK `Open` does not restore a San session identity.

San's opened `Session`, event adaptation, titles, tasks, groups, and UI projections
remain product responsibilities. SDK saves use native message structs and record
a truncate to the unchanged prefix followed by changed messages. SDK `Stat`
provides identity and per-session size/modification metadata. SDK `Store.Close`
is a no-op for file stores; writers must close separately. `Writer.Sync` explicitly
syncs pending writes.

## Tests and pitfalls

`jsonl_lifecycle_test.go` covers native fidelity, compaction/history, cross-store
lease handoff, and fork asset ownership after source deletion.
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

Resume performance benchmarks: `go test ./internal/transcript -run '^$' -bench BenchmarkListSummaries -benchmem`,
`go test ./internal/app/input -run '^$' -bench BenchmarkSessionSelectorFilterAndRender -benchmem`,
and `go test ./internal/app -run '^$' -bench BenchmarkResumeRender -benchmem`.
`internal/transcript/replay_test.go` compares streaming restoration and summaries
against full projection across replacement, compaction, state clearing, and model
changes. `internal/app/input/session_selector_test.go` covers loading, cancellation,
stale results, filtering, cached previews, and resizing.


Run `make ci`. Always stop Runtime before closing its Session. Full-history reads are
for browsing; `Open` always restores active context with zero `ReadOptions`. Never
write UI rows or a read result back at the end of a turn.

## See also

- [Agent runtime](agent.md)
- [Native transcript log](transcript.md)
- [Inspector](inspector.md)
- [Package map](../reference/package-map.md)
