# PROP-0005: Session management and SQLite persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-07. Documentation first; the APIs and SQLite backend below are
not implemented. Current storage remains sdk-go JSONL logs partitioned by a
hash of cwd. This proposal changes persistence, not agent execution semantics.

Goals: preserve current scenarios, make ownership explicit, and keep the API and
schema small. Each fact has one authority. Add a table for an independent
lifecycle or demonstrated query need, not for every noun. Derived summaries are
allowed only with a defined source and rebuild rule.

## Model and ownership

```text
Project 1 ── N Workspace 1 ── N Session
                                  ├─ parent_session_id → parent agent session
                                  ├─ fork_source_id    → source session
                                  └─ 1 ── N Events (session_events, ordered by seq)
                                            ├─ Session: session.created / session.forked
                                            ├─ Messages: messages.appended / messages.replaced
                                            │    └─ Append or replace an ordered native message list
                                            ├─ Compaction: compaction.completed
                                            │    └─ Summary ID + source seq; text belongs to messages
                                            ├─ Config/state: config.updated / state.updated
                                            │    └─ Session overrides / title, todos, group hint
                                            ├─ Inference: inference.started / completed / failed
                                            └─ Tool/audit: tool.started / completed / failed,
                                                 notice.added, permission.requested / decided,
                                                 hook.completed, system.updated, tools.updated, skill.updated

Events ── projector ──┬─ History: display history, including pre-compaction messages
                     ├─ Snapshot: active context + durable state for resume
                     └─ Summary: rebuildable session-list metadata
```

Each event row belongs to exactly one session. Its `type` selects the typed
`payload`; the categories above are not separate tables. Messages are event
content, not the entire event model. `messages.appended` owns native conversation
content; `tool.completed` adds execution/presentation metadata, not a second copy
of tool-result content. Parent/fork links relate sessions; they do not share mutable event rows.
History and Snapshot are views of the same events, not independent saved copies.
Event means a durable persistence fact here, not every transient streaming/runtime
notification. `Event` is the proposed storage envelope; current code still uses
`transcript.Record` and requires an explicit adapter, not a blind type rename.
Event names follow `<object>.<past-tense action>`; Session ownership is already
in `session_id`. These are target names, not the current codec. Import maps legacy
types explicitly while preserving their meaning. Archive belongs to state; current
execution cwd belongs to session state, not home-workspace identity.

### Recording contract: complete facts, minimal duplication

Persist an event when it changes recoverable state, contributes user-visible
history, or explains a consequential execution/security outcome. Do not record
every runtime notification. Each type needs a producer, consumer and minimum
payload; a type without one of those has not justified persistence.

| Event family | Minimum durable information |
| --- | --- |
| `session.created` / `session.forked` | Identity, initial location/settings, parent or fixed fork provenance |
| `messages.appended` / `messages.replaced` | Ordered native message list with stable IDs; one message is a one-element list |
| `compaction.completed` | Operation ID, source sequence and summary message ID; no second summary text |
| `config.updated` / `state.updated` | Only changed typed fields; distinguish absent from explicit clearing |
| `inference.started` / `inference.completed` / `inference.failed` | Attempt ID, input message IDs and non-secret request settings; outcome, usage and bounded error details |
| `tool.started` / `tool.completed` / `tool.failed` | Call/attempt IDs, execution outcome and necessary timing/presentation fields; arguments/results reference native messages or assets |
| `permission.requested` / `permission.decided`, `hook.completed` | Correlation, scoped decision/outcome and bounded reason; no duplicate prompts or tool payloads |
| `system.updated`, `tools.updated`, `skill.updated`, `notice.added` | Changed definitions/state or meaningful notice with its message position; unchanged definitions are not repeated per inference |

Persist inference/tool start before dispatch and correlate the terminal outcome;
retries have distinct attempt IDs. Cancellation is an explicit outcome, not success.
A start without an outcome means interrupted/unknown, not proven failure or a safe
automatic retry. This execution boundary is separate from a durable assistant tool
call, which may exist before execution is authorized. If the required start cannot
be committed, do not dispatch the operation. Adapter work must establish these
target guarantees rather than assume current callbacks already provide them.

Do not store token deltas, spinner/progress ticks, picker navigation, repeated reads,
empty patches, unchanged snapshots or reconstructible UI text. Final native messages
remain complete. Preserve genuine repeated actions and identical user messages:
deduplicate retries by event/operation ID, never solely by text or timestamps.
Message batches deduplicate by native message identity. Replacements carry the full
required active sequence; this intentional snapshot is not a reason to create a
parallel messages table. Secrets, credentials and unbounded diagnostic dumps are
excluded; retain bounded diagnostics and necessary asset references instead.

### Ownership rules

| Concept | Owns |
| --- | --- |
| Project | Stable logical identity, name and description |
| Workspace | Local directory/checkout and project association |
| Session | Durable conversation, home workspace and write ownership |
| Event | Immutable ordered conversation and application fact |
| Projection | Rebuildable interpretation of a committed record prefix |
| Runtime | Execution, cancellation and process-local resources |

Use stable opaque IDs, not paths, branches or remote URLs. New Git workspaces
resolve at worktree roots; non-Git roots are explicit (default: startup cwd).
Legacy exact-cwd registrations take precedence and remain separate until explicit
reconciliation. Do not silently merge clones, old partitions or trust decisions.
Relocation validates the new path and trust without rewriting historical cwd.

A session's home workspace is fixed, but execution cwd can change across projects
without creating another session. Reload tools, settings, hooks and trust for the
execution cwd. Restoring that location requires typed cwd-change records; old
history falls back to initial cwd. Missing workspaces do not prevent inspection.

Group roster/inbox/membership remain owned by the group subsystem. Session records
are restoration hints: resume must reclaim and reconcile; switching releases live
membership and fork clears inherited membership. Durable todos are not running
tasks. Replay never restarts processes or recreates callbacks; child history
survives without promising post-restart continuation. Continuing a live child
validates parent/task/session ownership. Late callbacks remain bound to their
original session, not the selected UI.

## API

These are proposed capability boundaries, not mandatory new packages. Final Go
types must align with sdk-go; consumers depend only on the methods they use.
All I/O accepts context cancellation. The following omits context parameters on
catalog methods only for readability.

| Owner | Operations | Contract |
| --- | --- | --- |
| Projects | `Resolve(location)`, `Get(id)`, `List(query)`, `Update(id, patch)`, `Archive(id, bool)` | Conservative registration; metadata, not configuration |
| Workspaces | `Resolve(location)`, `Get(id)`, `List(query)`, `Relocate(id, path)`, `Archive(id, bool)` | Validated location and association; no implicit merge |
| Settings | `Resolve(ctx, target) (ResolvedConfig, error)`, `Update(ctx, target, patch) error` | Explicit scope, typed patch and source provenance |

```go
type SessionStore interface {
    Create(context.Context, CreateOptions) (*Session, error)
    Open(context.Context, string) (*Session, error)
    Get(context.Context, string) (Summary, error)
    List(context.Context, SessionQuery) (SessionPage, error)
    History(context.Context, string, HistoryQuery) (HistoryPage, error)
    Fork(context.Context, string, ForkOptions) (*Session, error)
    Archive(context.Context, string, bool) error
    Delete(context.Context, string) error
}

// Internal storage capability for replay, inspection and export.
type EventReader interface {
    Events(context.Context, string, EventQuery) (EventPage, error)
}

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error
func (s *Session) Compact(ctx context.Context, change CompactionChange) error
func (s *Session) Snapshot() (Projection, error)
func (s *Session) Close() error
```

| Type / operation | Required behavior |
| --- | --- |
| `CreateOptions` | Workspace ID, initial cwd, optional ID/parent and non-secret initial settings |
| `Create` / `Open` | Return a writer-owned handle; Open claims before loading a consistent projection |
| `Get` / `List` | Read summaries without claiming a writer, including sessions owned by other processes |
| `SessionQuery` | Project/workspace/parent/group/sidechain/archive filters, bounded limit, opaque cursor |
| `Events` | Ordered raw events; cursor binds filters and fixed `ThroughSeq` so appends do not disturb pagination |
| `History` | Display projection of that fixed prefix, including pre-compaction history; attachments by reference |
| `Snapshot` | Immutable committed context, application state and recovery positions; no UI rows or live resources |
| `ForkOptions` | Optional destination workspace and source sequence; otherwise capture latest committed prefix |
| `CompactionChange` | Expected source sequence, stable operation ID, native summary and ordered retained messages |
| `Append` / `Compact` | One shared atomic write implementation; Compact validates the completed transition |
| `Archive` / `Delete` | Archive appends state; hard deletion requires ownership and rejects an active writer |
| `Close` | Wait for in-flight writes and release ownership exactly once |

Already-open sessions archive through their own Append. `Archive(..., false)`
restores visibility. Project/workspace deletion is restricted while children exist;
archive does not delete history. Parent relationships must be acyclic.

Project history before paginating display entries: isolated raw event pages cannot
reconstruct replacements. Never resume from UI history. List is a live keyset
listing ordered by `(updated_at, id)`, not a cross-request snapshot.

Resume coordinates Open → projection → current configuration/trust → runtime.
TUI, headless and ACP share ownership rules. ACP validates requested cwd against
the restored execution location before constructing services; mismatches require
an explicit permitted cwd change. Failed opens release ownership; failed switches
leave selection intact. Distinguish missing, busy, closed, conflict, invalid,
unsupported schema, corruption, cancellation and storage errors.

## Configuration and storage allocation

| Data | Authority / destination |
| --- | --- |
| Project description and workspace registration | `projects` / `workspaces` |
| Session identity and query summaries | `sessions`; summaries derive from records |
| Native messages, compaction, session overrides, todos, notices and audit | Typed `session_events.payload` |
| User defaults and provider/skill choices | Existing user files, including `~/.san/settings.json` |
| Shared project settings and instructions | Existing project files, including `.san/settings.json` |
| Local/worktree/persona overrides | Existing scoped files and resolver |
| CLI/environment overrides | Invocation memory; do not persist wholesale |
| Request settings used | Allowlisted non-secret fields in inference records, not future configuration authority |
| Secrets and security policy | Existing auth/secret/policy owners, not history or checkpoints |
| Large tool outputs and images | Files referenced by records |
| Optional recovery cache | `session_checkpoints` |

Settings writes select a scope and update only its fields, never the merged
result. Session-setting writes delegate to the owned Session handle; file writes
retain cross-process coordination. Patches distinguish absent, explicit empty/false
and removal where the source format supports it. Read-only sources reject writes.
Resolution returns per-field provenance, including multiple sources for merged values.

Preserve current user → project → local file resolution, compatibility sources,
linked-worktree main-checkout local overrides, persona and request behavior.
Merging is field-specific: maps merge per key, permission rules use deduplicated
unions, and hooks follow event-specific rules. Session selections cannot override
security checks. The detailed current order remains in
[configuration](../reference/configuration.md).

Resume resolves current policy and credentials; historical grants are not new
authorization. Missing models/providers require an explicit error or selected
replacement. Config file writes stay atomic; no cross-file/SQLite transaction is
promised. Request records describe settings actually used, while changes affect
subsequent resolutions. Do not create editable database mirrors of configuration
files. Moving their authority into SQLite requires a separate migration decision.

## Tables

Logical schema, not executable migrations. IDs are TEXT; times are UTC Unix
milliseconds. Columns are NOT NULL unless marked `?`. Enable foreign keys on every
connection. JSON payloads are typed and versioned, not arbitrary metadata bags.

### `projects`

| Column | Type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK | Stable project identity |
| `name` | TEXT | Display name |
| `description` | TEXT DEFAULT '' | Description, not implicit model instructions |
| `created_at`, `updated_at` | INTEGER | Registration / metadata modification |
| `archived_at` | INTEGER ? | Visibility marker |

No current cwd, model, branch or active-session column.

### `workspaces`

| Column | Type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK | Stable local identity |
| `project_id` | TEXT FK projects, DELETE RESTRICT | Logical owner |
| `root_path` | TEXT UNIQUE | Normalized local root |
| `kind` | TEXT CHECK directory/git_worktree | Location kind |
| `created_at`, `last_opened_at` | INTEGER | Registration / latest open |
| `archived_at` | INTEGER ? | Visibility, not filesystem deletion |

Index `(project_id, id)`. Path uniqueness includes archived rows; path reuse needs
explicit reconciliation. Resolution and trust share normalization and symlink
rules; do not approximate filesystem case behavior by lowercasing paths.

### `sessions`

| Column | Type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK | Conversation identity |
| `workspace_id` | TEXT FK workspaces, DELETE RESTRICT | Immutable home workspace |
| `parent_session_id` | TEXT ? FK sessions, DELETE SET NULL | Agent ancestry |
| `fork_source_id` | TEXT ? FK sessions, DELETE SET NULL | Fork provenance |
| `fork_source_seq` | INTEGER ? CHECK >= 0 | Captured source prefix |
| `initial_cwd` | TEXT | Historical launch location |
| `created_at`, `updated_at` | INTEGER | Creation / committed activity |
| `archived_at` | INTEGER ? | Derived archive state |
| `title` | TEXT DEFAULT '' | Derived display title |
| `last_seq` | INTEGER DEFAULT 0 CHECK >= 0 | Latest committed sequence |

Indexes: `(workspace_id, updated_at, id)`, `(parent_session_id)`, `(fork_source_id)`.
Project queries join workspaces rather than duplicate `project_id`. Birth/fork
records retain historical identities after reference columns are cleared on
source deletion; descendants do not cascade-delete. `initial_cwd` may be a
subdirectory, so it is not redundant with the workspace root.

Finalize model/preview/group/sidechain summary columns against existing queries
before implementation. Every derived column has a projector and no independent
setter; do not drop current filtering behavior to minimize column count.

### `session_events`

| Column | Type / constraint | Meaning |
| --- | --- | --- |
| `session_id` | TEXT FK sessions, DELETE CASCADE | Owning conversation |
| `seq` | INTEGER CHECK > 0 | Commit order within session |
| `id` | TEXT | Stable event identity |
| `type` | TEXT | Event discriminator |
| `schema_version` | INTEGER CHECK > 0 | Payload codec version |
| `created_at` | INTEGER | Event time, not ordering key |
| `payload` | TEXT, validated JSON | Complete event-specific content |

PK `(session_id, seq)`; UNIQUE `(session_id, id)`. Do not repeat the envelope in
payload without a codec requirement. Native message IDs differ from record IDs.
The codec must preserve signatures, encrypted reasoning, image/content order,
tool IDs and supported provider fields. Current `Record.Message`/`Messages` use
`json:"-"`; directly marshaling Record would lose messages. Preserve current
replacement semantics; introducing a message-parent tree is a separate decision.

### `session_checkpoints` — future option, outside phase one

Retained as a future schema reference, not an initial table or resume dependency.
Phase one restores from events; compaction is fully supported without checkpoints.

| Column | Type / constraint | Meaning |
| --- | --- | --- |
| `session_id` | TEXT PK FK sessions, DELETE CASCADE | One latest checkpoint |
| `through_seq` | INTEGER CHECK >= 0 | Complete projected prefix |
| `projector_version` | INTEGER CHECK > 0 | Projection and snapshot format |
| `payload` | BLOB | Complete durable projection, excluding secrets/live resources |
| `created_at` | INTEGER | Snapshot time |

Add only after measuring recovery costs. Validate `through_seq <= last_seq` in
a consistent snapshot; older rebuilds cannot replace newer checkpoints. Include
durable todos and recovery positions, not only messages. Invalid/incompatible
checkpoints fall back to records. No extra index beyond the PK is needed.

No initial parallel `session_state`, `project_state`, `messages`, `parts`, `runs`
or generic settings tables. SQL migration, record codec and projector versions
are separate contracts; database schema migrations need a version ledger.

## Compaction: first-phase session capability

Compaction changes active context; checkpointing only accelerates recovery.
`Session.Compact` is required from the first phase and uses the shared append path.
Summary generation belongs to the agent/runtime, outside any SQL transaction.

| Artifact | Storage |
| --- | --- |
| Original messages and referenced assets | Retained records/files for history, inspection, fork and export |
| Completed native summary and retained messages | `session_events`: `messages.replaced` payload |
| Summary ID and source sequence | `session_events`: `compaction.completed` payload |
| Partial summary tokens | Runtime memory; never accepted recovery state |

Compact accepts a fixed source sequence and stable operation ID. Validate that
the source is unchanged, then commit replacement, boundary and summaries in one
batch. Derive stable record IDs from the operation ID: identical retries succeed,
conflicting reuse fails. Check for an already committed operation before rejecting
a stale source sequence. Reject results from retired session/runtime owners.

The boundary references the summary already in the replacement; do not duplicate
its text in another record or table. Import must also support the existing
summary-append/boundary/truncate form and preserve native message IDs.

Failure before commit leaves the old context intact; commit before a crash makes
the new context authoritative even if never rendered. Consecutive compactions
retain earlier history and deduplicate native message IDs. Auto, manual and
inactive-runtime compaction use the same transition. Fork copies the chosen
prefix and compaction state independently. Compaction never deletes old assets;
retention/redaction is a separate policy. No dedicated `compactions` table is needed.

## Consistency and lifecycle

- Recommend one local database per San data home. Confirm driver/path in phase
  one; network filesystems are outside initial guarantees. Use WAL, bounded busy
  waits and an explicit durability policy. SQLite still has one concurrent writer.
- Append atomically inserts records and updates summaries/sequence, then publishes
  the committed memory snapshot. Same record IDs/content are idempotent; reusing
  an ID with different content is a conflict. Ambiguous commit results must be
  reconciled before new writes. Cancellation does not prove
  rollback. Unknown state-affecting record versions block resume.
- Keep per-session cross-process ownership independent of SQL transactions; use
  an SDK-supported mechanism not bound to JSONL paths. Close/process death release
  ownership. Never hold a transaction across inference, tools or approval, or use
  unfenced timestamp-only leases. External tool execution is not exactly-once.
- Publish assets atomically before references. Fork protects a fixed source prefix
  against deletion, copies assets and rewrites references before publishing the
  child. Clear inherited group membership. Cleanup excludes in-progress operations
  and is retryable. Delete commits before asset reclamation.
- Back up a consistent SQLite snapshot and referenced assets together. Copying only
  the main database file during WAL writes is insufficient.

## Ownership boundary and rollout

sdk-go owns generic ordered storage, native codecs, ownership and transaction
primitives. San owns identity resolution, configuration/trust, application records
and projections. Define atomic SDK/San summary updates without SDK importing San
or UI/runtime issuing SQL. Replace `TranscriptPath` and file-existence assumptions
with queries/export; JSONL remains interchange, not a second writable authority.

1. **Agree contracts:** audit sdk-go and existing callers; settle identity rules,
   query-driven columns, codecs, ownership, database scope and atomic projection.
   Include Compact, history/resume/fork and failure semantics now.
2. **Introduce API seams:** preserve the file-backed behavior while removing path
   assumptions; unify projection and compaction entrypoints. Identify stronger
   atomicity that requires the new backend.
3. **Implement SQLite:** entities, records, compaction and summaries; run shared
   behavior cases against both backends during development.
4. **Import and cut over:** exclusively own sources, preserve IDs/assets and old
   files, record import progress/provenance, and verify retries rather than overwrite.
   Report corrupt/unsupported sessions individually. Never silently merge projects.
5. **Optimize:** measure before adding checkpoints/indexes; retire legacy writers
   after verified cutover and update package documentation.

Only one backend writes a migrated session. Before new SQLite writes, originals
permit rollback; afterward export newer history before rollback. Migration
bookkeeping schema follows the codec/legacy-ID audit. Classify old parent fields
from original records rather than guessing whether they mean ancestry or fork.

## Acceptance and remaining decisions

| Scenario | Required result |
| --- | --- |
| Roots, nested cwd, clones/worktrees and legacy partitions | Predictable identity; no silent merging or trust inheritance |
| Move, missing directory, path reuse, cross-project `cd` | Inspectable history; explicit reconciliation; execution policy refreshed |
| TUI/headless/ACP and competing processes | Validated cwd and common single-writer ownership |
| Group switch/rejected reclaim; late background completion | No resurrected membership or cross-session callbacks |
| Child history and fork; parent deletion | Independent identity/assets; history does not imply a live task |
| Replacement, repeated/failed compaction and retry | Correct native context, atomic transition, preserved original history |
| Configuration changes and revoked permissions | Correct scoped writes and current security policy on resume |
| Display history, images and parallel tools | Complete browsing without expanding resumed model context |
| Crash, duplicate writes, cleanup and bad checkpoints | Consistent records/summaries; recoverable caches; explicit failures |
| Interrupted calls, retry and repeated identical input | Starts/outcomes correlate; unknown execution stays unknown; distinct actions survive deduplication |
| Streaming/progress and unchanged state | Complete final messages without per-token events, empty patches or repeated snapshots |
| Import, repeated import, export and rollback | Preserved IDs/content and one writable authority |

Use existing session/transcript/app tests as the baseline. Check picker responsiveness
and replay costs before optimization. Documentation does not claim SQLite tests pass.

Before implementation, settle relocation/worktree-linking UX, final summary fields,
SDK transaction integration and migration bookkeeping. JSONL remains a valid
alternative if transaction/query benefits do not justify migration. An SQLite index
over files retains cross-store consistency work; per-project databases isolate
failures but complicate linking and global queries. Avoid speculative schemas.
