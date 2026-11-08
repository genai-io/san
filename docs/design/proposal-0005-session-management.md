# PROP-0005: Session management and SQLite persistence

## Status

Draft — documentation-first design, 2026-11-07. No SQLite backend or new API
below is implemented. Package documents describe the current file-backed system.
Implementation proceeds in reviewable phases after the contracts are agreed.

## Motivation and goals

Make project ownership, session lifecycle, history, and recovery explicit before
replacing transcript files with SQLite. Cover real coding-agent scenarios without
creating duplicate authorities or a table for every noun.

- One owner for each fact; derived summaries and checkpoints are rebuildable.
- Small APIs independent of SQL, file paths, terminal rendering, and transports.
- Preserve native messages, compaction, fork, child agents, tasks, and inspection.
- Separate configuration intent, durable history, live execution, and caches.
- Keep generic persistence in sdk-go; San owns project policy and projections.

A table needs an independent identity/lifecycle or a demonstrated query purpose.
A column needs an owner and an update rule. Derived duplication is acceptable only
with a stated source and rebuild rule; two independently editable copies are not.
Cover today's scenarios explicitly, but extend through typed/versioned contracts
rather than speculative entities or an unrestricted metadata bag.

Non-goals: a general database framework, distributed execution, automatic tool
re-execution, a new branch-navigation model, moving secrets into transcripts, or
converting every application file into a database row.

## Current implementation and references

Today `session.Store` is bound to a cwd-derived project directory. `ProjectID`
hashes a cleaned absolute path; it is a storage partition, not a logical project
identity. An opened `Session` holds a writer lease. `transcript.Store` adapts
sdk-go logs; messages and conversation replacements retain native SDK types.
Resume replays the active conversation after resets; display history retains
messages from before compaction. Metadata sidecars are disposable caches.

Reference implementations inspected on 2026-11-07:

- [Pi SessionManager](https://github.com/badlogic/pi-mono/blob/085c84e98e7f4c950f2be606466fff4f75d8fe7c/packages/coding-agent/src/core/session-manager.ts):
  JSONL entries, `getEntries`, `getBranch`, `buildContextEntries`, and a canonical
  `buildSessionProjection`; `buildSessionContext` extracts messages and settings.
  Borrow the common projection, not necessarily its tree model or sync I/O API.
- [OpenCode session schema](https://github.com/anomalyco/opencode/blob/93c65afbf0c8e64038a257dfeead99c21179654f/packages/core/src/session/sql.ts)
  and [project schema](https://github.com/anomalyco/opencode/blob/93c65afbf0c8e64038a257dfeead99c21179654f/packages/core/src/project/sql.ts):
  SQLite entities, ordered messages and parts, project relationships, and durable
  execution information. Its schema is evidence, not a schema to copy wholesale.
- [Claude Code documentation](https://code.claude.com/docs/en/cli-reference):
  JSONL transcript paths are supported for resume. This is a public contract,
  not a review of its private persistence implementation.

SQLite is justified by transactions and queries, not by JSONL being obsolete.
Moving a full replay to SQL alone does not remove its O(N) cost.

## Domain and ownership

```text
Project 1 ── N Workspace 1 ── N Session 1 ── N Record
                                  │
                                  ├─ parent session: agent ancestry
                                  └─ fork source: copied history provenance
Records ── projector ── context / display history / application state
```

| Concept | Owns | Does not own |
| --- | --- | --- |
| Project | Stable logical identity, name, description | Current cwd, Git branch, live agent status |
| Workspace | One local checkout/directory and project association | Conversation history or project-wide trust |
| Session | Durable conversation identity, workspace, lifecycle | A process, model connection, or UI selection |
| Record | Immutable ordered facts within a session | Project settings or all application data |
| Projection | Interpretation of a fixed record prefix | Independent editable facts |
| Runtime | Execution, cancellation, pending interactions | Persistence identity or SQL transactions |

### Identity rules

Use opaque stable IDs. Paths locate workspaces; remote URLs and branches do not
identify projects. Resolve an exact registered legacy cwd first. Otherwise resolve
a new Git checkout at its worktree root and a non-Git workspace at an explicitly
selected root (default: startup cwd). A subdirectory session keeps its own initial
cwd. Do not infer arbitrary non-Git parent projects. Legacy cwd-root registrations
may coexist with a new checkout-root workspace; unifying them requires an explicit
mapping, not a uniqueness violation or silent migration merge.

Multiple worktrees may belong to one project, but sharing a remote does not merge
clones, configuration, or trust. Initial migration preserves existing partitions;
project linking and relocation require explicit operations. Missing directories
remain browsable historical workspaces. Relocation preserves IDs only after
validation, must not rewrite historical cwd values, and requires a fresh trust
check. Sessions keep their original **home workspace** association, but execution
cwd may change, including across checkout boundaries, without changing identity
(as today's `changeCwd` allows). Re-resolve settings, tools, hooks and trust for
the execution cwd. Record validated cwd changes as typed events if restoring the
latest execution location; old transcripts fall back to their initial cwd. Moving
the home association itself is not part of ordinary `cd` or backend migration.

Project/workspace archive hides entries by default without deleting history.
Hard removal is restricted while children exist; bulk purge requires a separate,
explicit operation. A session cannot be its own parent; creation must prevent
ancestry cycles. Ancestry need not imply the same workspace.

### Configuration and state

| Information | Authority |
| --- | --- |
| Project name/description; workspace registration | Database entity rows |
| Shared project instructions/settings; user/local settings | Existing configuration files and resolver |
| Credentials | Existing secret/auth stores, never history payloads |
| Session model/mode/durable todo changes | Typed session records |
| Group roster, inbox and live membership | Existing group subsystem; session records are restoration hints |
| Background processes and continuation callbacks | Process-local task/runtime owners, not replayed history |
| Git HEAD, branch, dirty state | Git; historical annotations are observations |
| Live writer ownership | Cross-process ownership mechanism, not a `running` flag |
| List summaries and optional resume checkpoint | Derived database projections |

Keep existing configuration precedence and trust filtering. The proposed settings
resolution contract returns effective values and their provenance for the selected
execution cwd and session overrides. Resuming reloads current policy and credentials;
historical permission
settings never authorize new operations. Project descriptions are not implicitly
system prompts. Store only non-secret execution settings needed to explain a
request; do not snapshot environment variables or credentials wholesale.

Do not duplicate merged settings in `projects.config_json`. A future
`project_preferences` table is warranted only for typed, database-owned local
preferences with no existing file authority. Do not pre-create empty preference,
state, or generic key/value tables for symmetry.

Group replay must reclaim membership through the group subsystem and reconcile a
rejected reclaim (kicked member or disbanded group); it cannot recreate authority
from a historical record. Switching releases live membership, while fork clears
inherited membership. Durable todo items are distinct from running background
tasks: replay/checkpoints do not restart processes or recreate process-local
continuations. Child transcripts remain inspectable after restart; continuing a
live child still validates parent/task/session ownership. Late callbacks stay
bound to their original session and cannot mutate the newly selected UI.

### Configuration scopes and write destinations

SQLite session storage does not imply SQLite ownership of all settings. Keep the
same logical settings API regardless of storage medium; explicitly choose a scope
on write, never save the fully merged result back into any source.

| Scope/data | Authoritative destination | Read/write behavior |
| --- | --- | --- |
| User defaults | `~/.san/settings.json` and existing user-level provider/skill configuration files | Shared across projects; update only the selected file's fields |
| Project shared settings | Selected cwd's `.san/settings.json` and existing instruction/config files | Repository-editable and shareable; not duplicated in `projects` |
| Local/worktree settings | Existing `settings.local.json` locations, including main-checkout local files for linked worktrees | Personal overrides; preserve current resolution and trust rules |
| Persona overlay | Selected persona's settings files | Existing field-specific overlay; not a project-row copy |
| Session overrides | Typed `session.state.patched` records in SQLite | Apply only to supported session fields; do not rewrite user/project defaults |
| CLI/environment overrides | Invocation memory | Do not persist the entire environment or silently promote temporary values |
| Request configuration history | Allowlisted fields in inference/session records | Evidence of the request, not editable defaults or future permission grants |
| Credentials, trust and permission authority | Existing secret/auth and security-policy owners | Not copied into ordinary settings JSON or checkpoint payloads |

The current settings-file order is Claude-compatible user → San user →
Claude-compatible project → San project → project-local Claude → project-local
San; linked worktrees then read main-checkout local Claude and San files. Defaults
seed resolution, and persona/request overlays follow their current call paths.
This is not a universal last-writer-wins rule: maps merge per key, permission
rules use deduplicated unions, and hooks have native-over-compat event rules.
Session model selection and explicit request options cannot override safety
checks. Preserve these rules and test them rather than introducing a single
JSON merge for every configuration family.

Proposed settings capability (separate from SessionStore):

```go
Resolve(ctx context.Context, target ConfigTarget) (ResolvedConfig, error)
Update(ctx context.Context, target ConfigTarget, patch SettingsPatch) error
```

`ConfigTarget` identifies user, project or local-file scope plus validated cwd,
or an owned session for supported session overrides. `SettingsPatch` is typed and
distinguishes absent, explicit empty/false, and removing an override where the
underlying format supports it; unsupported clearing operations must fail clearly.
`ResolvedConfig` carries effective values and per-field source provenance (multiple
sources for merged fields), excluding secret material from inspection output.
Read-only sources reject Update. Session writes delegate to the opened handle;
file writes retain atomic updates and cross-process coordination. No transaction
is promised across config files and SQLite: request records capture the settings
actually used. Changes affect subsequent resolutions, not an in-flight request
or prior transcript; reload/cwd changes must not leave stale permissions cached.

On resume, restore supported session selections, then resolve current file and
policy sources. A missing historical model/provider produces an explicit error or
user-selected replacement, not silent replay under a different model. Removed
permissions take effect even when an old record shows a grant. If user/project
settings ever move into SQLite, that is a separate authority migration with
explicit import/export and file compatibility, not an extra writable mirror.

## API contracts

These are conceptual product APIs, not a prescription for new packages or
provider-side interfaces. Final Go signatures must be reconciled with sdk-go
before implementation. Use context cancellation for I/O and typed options/results.

### Proposed Go surface

The following signatures specify capability boundaries. They are design notation,
not code to copy into a new universal interface package; consumers should depend
only on the methods they use. IDs are strings here; concrete named ID types can
be adopted when aligning with the SDK.

```go
type ProjectCatalog interface {
    Resolve(context.Context, ProjectLocation) (Project, error)
    Get(context.Context, string) (Project, error)
    List(context.Context, ProjectQuery) (ProjectPage, error)
    Update(context.Context, string, ProjectPatch) (Project, error)
    Archive(context.Context, string, bool) error
}

type WorkspaceCatalog interface {
    Resolve(context.Context, WorkspaceLocation) (Workspace, error)
    Get(context.Context, string) (Workspace, error)
    List(context.Context, WorkspaceQuery) (WorkspacePage, error)
    Relocate(context.Context, string, string) (Workspace, error)
    Archive(context.Context, string, bool) error
}

type SessionStore interface {
    Create(context.Context, CreateOptions) (*Session, error)
    Open(context.Context, string) (*Session, error)
    Get(context.Context, string) (Summary, error)
    List(context.Context, SessionQuery) (SessionPage, error)
    ReadRecords(context.Context, string, RecordQuery) (RecordPage, error)
    ReadHistory(context.Context, string, HistoryQuery) (HistoryPage, error)
    Fork(context.Context, string, ForkOptions) (*Session, error)
    Archive(context.Context, string, bool) error
    Delete(context.Context, string) error
}

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, records ...Record) error
func (s *Session) Compact(ctx context.Context, change CompactionChange) error
func (s *Session) Snapshot() (Projection, error)
func (s *Session) Close() error
```

| Input/result | Minimum contract |
| --- | --- |
| ProjectLocation / WorkspaceLocation | Validated root/cwd and explicit association if supplied; resolution may register an identity, never silently merge existing ones |
| ProjectPatch | Typed optional name/description; absent leaves unchanged, empty explicitly clears where allowed |
| CreateOptions | Workspace ID, initial cwd, optional caller-assigned session ID, parent identity and initial non-secret settings |
| ForkOptions | Optional destination workspace and source sequence; omitted sequence captures latest committed prefix; destination cwd validated before creation |
| CompactionChange | Expected source sequence, stable operation ID, native summary message and ordered retained messages; no model callback or UI data |
| SessionQuery | Project/workspace/parent/group/sidechain/archive filters as required by current callers, bounded limit and opaque cursor |
| RecordQuery / HistoryQuery | Bounded limit and opaque cursor; no `includeCompacted` mode switch between history and resume |
| RecordPage / HistoryPage | Typed records or display entries, `ThroughSeq`, `NextCursor`; history entries retain source IDs and attachment references |
| Projection | `ThroughSeq`, native active messages, typed application state, inference/exchange positions and unfinished-operation information |
| Summary / SessionPage | Picker metadata only; page contains summaries and next cursor, never hydrated tool blobs |

Project/workspace queries similarly use bounded pages and archive filtering.
`Archive(..., true)` archives; `false` restores visibility. Archiving a session
claims a temporary writer and appends a typed state event; an already opened
session performs that change through its own Append. Busy ownership is an error,
not an implicit steal. Invalid project/workspace filter combinations are rejected.

### Behavior and ownership

| Owner | Operation | Contract |
| --- | --- | --- |
| Projects | Resolve / Get / List / Update / Archive | Resolve identity conservatively; update descriptions, not settings |
| Workspaces | Resolve / Get / List / Relocate / Archive | Validate location and project association; no silent merges |
| Sessions | Create(options) | Require workspace ID; persist initial records and return owned handle |
| Sessions | Open(id) | Claim writer before loading a consistent projection; return owned handle |
| Sessions | Get(id) / List(query) | Read summaries without claiming a writer; filter by project/workspace/parent/archive |
| Sessions | ReadRecords(id, query) | Read a stable ordered record prefix for inspection/rebuild |
| Sessions | ReadHistory(id, query) | Read-only display query, not a second persistence model |
| Sessions | Fork(id, options) | Copy a fixed source version into an independent owned identity |
| Sessions | Archive(id) / Delete(id) | Archive via record; hard delete claims ownership and rejects an active writer |
| Session | ID / Snapshot | Identity and immutable committed projection with its sequence |
| Session | Append(records...) | One atomic batch and one persistence entrance |
| Session | Compact(change) | Validate and commit completed compaction through the same append implementation; never invoke the model inside a transaction |
| Session | Close | Release writer exactly once, after in-flight writes |

Do not add parallel `SaveMessages`, `SaveHistory`, and `SaveMetadata` authorities.
Convenience methods produce typed records through Append. Snapshot contains the
native active context, application state and recovery positions, not UI rows or
live runtime resources. It reflects committed state, not uncommitted token deltas.

`ReadRecords` returns records, a high-water sequence and an opaque next cursor.
The cursor binds session, ordering, filters and high-water position. Append-only
pagination is stable while new writes arrive; deletion invalidates the cursor.
`ReadHistory` uses the same fixed prefix but projects display entries, with large
attachments returned by reference. Project before paginating display entries;
raw event pages cannot independently reconstruct compaction or replacements.
Neither API sends its display result back to the model. List pagination uses a
stable `(updated_at, id)` order but is a live listing, not a multi-request snapshot.

Resume is orchestration: Open → committed projection → current settings and trust
checks → runtime restoration. Normal execution and restoration share projection
rules. TUI, headless and ACP callers use the same ownership and storage contracts.
A global ID lookup is not authorization to construct a runtime in any cwd: ACP
load must validate the requested cwd against the restored execution location,
rejecting mismatches unless the caller explicitly requests an allowed cwd change.
Resolve all settings, extensions, hooks and permissions for that validated location
before execution; never mix saved history with an unrelated workspace's policy.
An interrupted tool call is not automatically safe to repeat: an external
side effect may have completed before its result was committed.

Distinguish not found, busy, closed, conflict, invalid input, unsupported schema,
corrupt data, cancellation, and storage failure. Failed opens release ownership;
failed switches leave the selected session untouched. Unknown state-affecting
record versions prevent resume, rather than being silently skipped.

## Logical schema

### Storage allocation

Every durable datum has a named destination. This is not a four-table limit:
introduce another table when it has independent ownership or a proven query need,
not merely because it appears in this inventory. File-backed authorities below
are deliberate scope decisions, not unassigned database work.

| Data | Table/column or file destination | Authority / reconstruction |
| --- | --- | --- |
| Project identity and description | `projects.id/name/description` | Authoritative entity metadata |
| Workspace registration and home project | `workspaces.root_path/project_id/kind` | Authoritative location registration |
| Session home, ancestry and fork provenance | `sessions.workspace_id/parent_session_id/fork_source_id/fork_source_seq`; birth/fork records | Row links to extant entities; records retain original historical identities |
| Session title/archive/list summary | `sessions.title/archived_at` and query-driven summary columns | Derived from records in the same commit |
| Messages, images-in-message and tool-call/result payloads | `session_records.payload`, typed message/replacement events | Native historical content; large asset contents remain referenced files |
| Compaction summary and retained active context | `session_records.payload`, `conversation.replaced` | Full replacement messages, including native summary ID/content |
| Compaction boundary and input version | `session_records.payload`, `session.compacted` | Summary ID plus source sequence, not duplicate summary text |
| Session model/mode/other supported overrides | `session_records.payload`, `session.state.patched` | Session-only intent, never a copy of all resolved settings |
| Durable todos, notices and tool presentation details | Corresponding typed `session_records.payload` | Projected into application state/history |
| Group restoration hint | Session state record; optional derived list column | Actual roster/inbox/membership remain in the group subsystem |
| Non-secret settings used for a request | Inference record payload | Historical evidence, not future configuration authority |
| User defaults and provider choices | Existing user configuration files | Config resolver; no `users` or user-config table in this migration |
| Shared Project settings/instructions | Existing project configuration/instruction files | Config resolver; no duplicate `project_settings` table |
| Worktree/local/persona settings | Existing scoped files | Preserve current precedence, trust and write destinations |
| Large outputs and pasted image bytes | Existing attachment files with record references | Session-owned assets; not dropped by compaction |
| Optional restored projection | `session_checkpoints.payload/through_seq/projector_version` | Disposable cache of records |
| Secret values and security-policy authority | Existing secret/auth/policy owners | Not a transcript or checkpoint responsibility |

Four core tables follow. SQL is specified as fields and constraints rather than
an executable migration; timestamp encoding is UTC Unix milliseconds. Opaque IDs
are TEXT. All foreign-key enforcement must be enabled on every connection.

### projects

| Column | SQL type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK NOT NULL | Stable project identity |
| `name` | TEXT NOT NULL | Display name |
| `description` | TEXT NOT NULL DEFAULT '' | Description, not implicit instructions |
| `created_at` | INTEGER NOT NULL | Registration time |
| `updated_at` | INTEGER NOT NULL | Metadata modification time |
| `archived_at` | INTEGER NULL | Hidden by default when set |

Project metadata is authoritative here, not repeated in every session event.
No current model, cwd, branch, or active-session column.

### workspaces

| Column | SQL type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK NOT NULL | Stable local workspace identity |
| `project_id` | TEXT NOT NULL FK projects, DELETE RESTRICT | Logical owner |
| `root_path` | TEXT NOT NULL UNIQUE | Normalized local root |
| `kind` | TEXT NOT NULL, CHECK directory/git_worktree | Location kind |
| `created_at` | INTEGER NOT NULL | Registration time |
| `last_opened_at` | INTEGER NOT NULL | Latest open time |
| `archived_at` | INTEGER NULL | Visibility, not filesystem deletion |

Unique normalized `root_path` within the local store, including archived rows:
archive/reopen must not accidentally create a second identity for the same path.
Path reuse by a different project needs explicit reconciliation. Index
`(project_id, id)`. `kind` initially distinguishes directory and Git worktree.
Path normalization and symlink handling must be shared by resolution and trust
checks; filesystem case behavior must not be approximated by lowercasing paths.

### sessions

| Column | SQL type / constraint | Meaning |
| --- | --- | --- |
| `id` | TEXT PK NOT NULL | Durable conversation identity |
| `workspace_id` | TEXT NOT NULL FK workspaces, DELETE RESTRICT | Immutable workspace association |
| `parent_session_id` | TEXT NULL FK sessions, DELETE SET NULL | Existing parent agent session |
| `fork_source_id` | TEXT NULL FK sessions, DELETE SET NULL | Existing source session |
| `fork_source_seq` | INTEGER NULL, CHECK >= 0 when set | Captured source prefix |
| `initial_cwd` | TEXT NOT NULL | Historical launch directory |
| `created_at` | INTEGER NOT NULL | Creation time |
| `updated_at` | INTEGER NOT NULL | Latest committed activity |
| `archived_at` | INTEGER NULL | Derived archive state |
| `title` | TEXT NOT NULL DEFAULT '' | Derived picker title |
| `last_seq` | INTEGER NOT NULL DEFAULT 0, CHECK >= 0 | Latest committed sequence |

Index `(workspace_id, updated_at, id)`, `(parent_session_id)`, and
`(fork_source_id)`. Project queries join workspaces; no duplicated `project_id`.
`initial_cwd` is a historical launch directory, not a copy of the workspace root.

Workspace association is immutable in this version. Session birth facts are
recorded at creation; title, archive state, update time and last sequence are
maintained transactionally. Reference columns point to surviving entities;
original ancestry/fork IDs and captured sequence remain in immutable records
after source deletion. Deletion never cascades through ancestry or fork links.

Add model/preview/group summary columns only when existing picker/group queries
require them; each must have a defined projector and no independent setter.
Do not lose current title, last-prompt, sidechain or group filtering behavior to
achieve an arbitrary column count. Final query-driven columns are a phase-one gate.

### session_records

| Column | SQL type / constraint | Meaning |
| --- | --- | --- |
| `session_id` | TEXT NOT NULL FK sessions, DELETE CASCADE | Owning conversation |
| `seq` | INTEGER NOT NULL, CHECK > 0 | Commit order within session |
| `id` | TEXT NOT NULL | Stable record identity |
| `type` | TEXT NOT NULL | Typed event discriminator |
| `schema_version` | INTEGER NOT NULL, CHECK > 0 | Payload codec version |
| `created_at` | INTEGER NOT NULL | Event time, not ordering key |
| `payload` | TEXT NOT NULL, validated JSON | Complete event-specific payload |

Primary key `(session_id, seq)`; unique `(session_id, id)`. Sequence orders
commits; timestamps do not. IDs support references and idempotency. The typed
payload contains event-specific data, not a duplicate mutable entity document.
Do not repeat the envelope in payload unless required by an explicit codec.

Define a versioned persistence codec: current `transcript.Record.Message` and
`Messages` have `json:"-"`; directly marshaling Record loses native messages.
Preserve content order, signatures, encrypted reasoning, images, message/tool
IDs, replacement boundaries and unknown provider fields supported by SDK types.
Record IDs and native message IDs are distinct. Retain current reset/compaction
semantics; do not invent a parent-record tree during the backend migration.

### Compaction storage and recovery

Compaction is a first-phase Session Management capability, required before a
backend can claim session conformance. It is not deferred with checkpoints or
owned by a separate compaction storage subsystem. `Session.Compact` accepts a
completed summary plus retained native messages and validates the transition;
the agent/runtime generates that summary outside persistence. Compact uses the
same append implementation, not an independent writer or second source of truth.

Compaction output is durable conversation data, not a disposable cache. No
separate `compactions` table or latest-summary file is needed for the initial
model: retain complete typed payloads in `session_records`.

| Artifact | Storage | Recovery meaning |
| --- | --- | --- |
| Original messages and tool references | Existing immutable records and referenced assets | Remain available to history, inspection, fork and export |
| Completed native summary and retained messages | A `conversation.replaced` payload in records | Defines the new active context without deleting earlier records |
| Compaction boundary | `session.compacted` payload referencing the summary's native message ID and source sequence | Identifies which committed context was compacted |
| Partial summary tokens | Runtime memory | Not an accepted replacement and never a resume checkpoint |
| Optional replay checkpoint | Derived `session_checkpoints` row | Accelerates projection; safe to discard and rebuild |

Generate the summary outside the write transaction from a fixed context sequence.
Before accepting it, validate that its input context has not changed; initially
use an expected source sequence and reject/recompute on conflict. Hold the session
writer, but no SQL transaction, while waiting on the model. Late results from a
retired session/runtime must be rejected, as in current manual-compaction handling.

For new SQLite writes, Compact derives stable record IDs from its operation ID
and commits the replacement (containing the native summary and any retained
messages), boundary and derived summaries in one Append batch. Repeating the same
operation/content returns success without another transition; conflicting reuse
fails. The expected source sequence is checked for a new operation after checking
whether it already committed, so a retry is not mistaken for a stale compaction.
The
boundary references the summary already present in the replacement; it need not
store another copy of its text. A separate `message.appended` copy is unnecessary
for this new batch shape. Replay accepts the current legacy summary-append,
boundary and truncate sequence during import, preserving its native message IDs.
The persistence codec/projector must explicitly support both shapes before cutover.

If generation fails, is cancelled, or the batch does not commit, the previous
context remains authoritative. If commit succeeds before a crash, resume uses the
replacement even if the UI never displayed it. Consecutive compactions apply in
sequence; history retains earlier summaries and original turns, deduplicating
native message IDs. Invalid boundaries fail validation rather than resurrecting
summarized-away context. Auto-compaction, manual compaction and manual compaction
before runtime startup must use the same durable transition.

A fork captures the source's history and compaction state at its chosen sequence;
compacting either conversation later cannot affect the other. Compaction alone
must not garbage-collect old tool results or images: full history still references
them. Any future retention/redaction policy is separate and must define its effect
on history, fork, export and checkpoints. Checkpoints never substitute for keeping
the summary records themselves.

### Optional derived tables

Create `session_checkpoints` only when recovery measurements justify it:

| Column | SQL type / constraint | Meaning |
| --- | --- | --- |
| `session_id` | TEXT PK NOT NULL FK sessions, DELETE CASCADE | One latest checkpoint per session |
| `through_seq` | INTEGER NOT NULL, CHECK >= 0 | Complete projected record prefix |
| `projector_version` | INTEGER NOT NULL, CHECK > 0 | Interpretation and snapshot-format version |
| `payload` | BLOB NOT NULL | Versioned complete durable projection, excluding runtime handles/secrets |
| `created_at` | INTEGER NOT NULL | Snapshot creation time |

Validate `through_seq <= sessions.last_seq` against the same database snapshot.
A rebuild must not replace a newer checkpoint with an older one. Checkpoints
include durable todo and recovery positions, not only messages; they do not store
live processes, callbacks or temporary grants. Incompatible/corrupt snapshots fall
back to records. Compaction changes context semantics; checkpointing only speeds
recovery. No separate index is needed beyond the session primary key.

No initial `session_state`, `project_state`, `messages`, `parts`, `runs`, or
untyped settings tables. Introduce one only for a demonstrated independent
lifecycle or query need. Schema migrations have their own version ledger;
SQL schema, record codec, and projector versions are separate contracts.

## Transactions, ownership, and files

Recommend one local database per San data home, so project/workspace relations
and cross-project queries share a transaction domain. Confirm path and driver in
phase one. Remote/network-filesystem databases are outside the initial guarantee.
Use WAL, bounded busy waits, short write transactions and an explicit durable
synchronization policy. Never keep a SQL transaction open across inference,
tool execution, or user approval. SQLite permits only one concurrent writer;
measure contention rather than assuming WAL solves it.

Append validates a batch, inserts records, updates summaries and last sequence,
then commits before publishing the new in-memory projection. A batch either
commits entirely or not at all. Stable record IDs are allocated before submission;
a retry with the same IDs/content is idempotent, different content is a conflict.
Ambiguous commit outcomes require reconciliation before accepting new writes.
Cancellation after commit cannot be treated as proof that nothing was written.

Keep per-session cross-process ownership separate from database transactions.
Prefer the existing ownership semantics, using an SDK-supported mechanism not
tied to a JSONL path; Close and process death release ownership. No timestamp-only
lease without fencing. Do not claim exactly-once execution of external tools.

Large tool outputs and images remain files. Publish assets atomically before
committing references; a crash may leave an unreferenced file, never a knowingly
missing committed asset. Fork copies assets and rewrites references before
publishing the child transaction, capturing a source prefix while preventing
concurrent source deletion. Clear inherited group membership. Failure cleanup
and orphan collection must exclude in-progress writers/forks and be retryable.
Delete commits removal before reclaiming assets. Foreign-key cascades do not
remove files. Backups use SQLite's consistent backup facilities plus referenced
assets; copying only the main database file during WAL writes is not a backup.

## SDK and San boundary

sdk-go should own reusable ordered storage, native message codecs, ownership and
transaction primitives. San owns project/workspace resolution, configuration and
trust policy, application records, summaries, and UI projections. Agree how San's
summary updates participate in the same transaction without SDK importing San
or exposing raw SQL to UI/runtime callers. This is a design gate, not permission
to maintain two independent stores or to reimplement SDK capabilities in San.

Replace path-as-identity consumers (`TranscriptPath`, file existence checks,
inspector and subagent exports) with queries or explicit export. JSONL remains
an interchange/debugging format, not a long-term dual-written authority.

## Migration and incremental delivery

1. **Contract review:** audit the pinned sdk-go APIs and all query consumers;
   settle identity resolution, summary fields, codec, ownership, database scope,
   driver and atomic SDK/San projection boundary. Include Compact, its payload,
   history/resume/fork semantics and interruption behavior in this first-phase
   contract; compaction is required, not a later performance enhancement.
2. **API seams:** remove file-path assumptions and separate display/history from
   restoration without changing the file-backed behavior. Route auto/manual
   compaction through the session capability, preserving existing messages and
   boundaries; identify any stronger atomicity that requires the new backend.
   Implement shared projection contracts with focused conformance tests.
3. **SQLite backend:** implement entities, records, atomic compaction transitions,
   transactions and summaries; run the same behavioral cases against both
   backends during development.
4. **Explicit import and cutover:** import supported current transcripts under
   exclusive source ownership, preserving identities and assets. Checkpoint the
   import at transaction boundaries; retries verify provenance/content rather
   than overwriting existing sessions. Preserve old files and report unsupported
   versions/corrupt sessions individually. Do not silently merge legacy projects.
5. **Performance and cleanup:** add checkpoints/indexes only after measurement;
   retire legacy writers after a verified cutover. Update package docs when shipped.

During cutover only one backend may accept writes for a migrated session. Rollback
before new SQLite writes can use preserved originals; afterward rollback requires
verified export of newer history, not simply reopening stale files. Migration
provenance/restart bookkeeping is required, but its exact schema follows the
codec and legacy-ID audit rather than adding a speculative core table now.

## Acceptance scenarios

| Scenario | Required outcome |
| --- | --- |
| Non-Git root, nested cwd, multiple clones/worktrees | Predictable resolution; no remote-based merging or trust inheritance |
| Move, missing directory, reused path | Explicit reconciliation; history remains readable |
| `cd` across project boundaries | Same session/home identity; execution context and policy reloaded |
| Two legacy cwd partitions inside one checkout | Exact legacy resolution preserved until explicit reconciliation |
| TUI/headless/ACP create and load | Same ownership; validated execution cwd before constructing runtime |
| Group switch, rejected reclaim, fork | Group subsystem remains authoritative; no resurrected membership |
| Background completion after switch or restart | Original-session routing; persisted child history does not imply a live task |
| Parent agent, child agent, independent fork | Distinct relationships; source deletion does not delete descendants |
| Replacement, compaction, retry, parallel tools | Same native restored context as the supported old backend |
| Auto/manual/inactive-runtime compaction, repeated compaction, failure and retry | Complete durable transition or unchanged prior context; original history and assets retained |
| User/project/local/session configuration changes | Writes target only the explicit authority; current policy and field-specific precedence survive resume |
| UI history and attachment hydration | Pre-compaction browsing without expanding model context |
| Append crash, duplicate retry, two processes | Atomic records/summaries; one session writer; no split ownership |
| Settings changes and revoked permissions | Current policy enforced; secrets absent from stored snapshots |
| Fork/delete crash and missing assets | No silent success; retryable cleanup and isolated failures |
| Unknown schema or bad checkpoint | Explicit resume error or safe checkpoint fallback |
| Import, repeated import, export and rollback | No lost IDs/content, no duplicate import, documented cutover boundary |

Use existing session/transcript/app tests as the baseline; add contract tests at
the owning layer. Verify read-only picker responsiveness and large-history replay
before enabling checkpoints. Documentation changes alone do not claim these
acceptance scenarios pass against SQLite.

## Alternatives and open decisions

Keep JSONL if transaction/query complexity does not justify migration. An SQLite
index over authoritative files is smaller initially but retains cross-store
consistency work. Message/part tables fit OpenCode but would rewrite San's event
semantics. Full Pi-style trees are a separate product decision. Per-project
databases isolate failures but complicate project linking and global queries.

Before implementation, resolve the phase-one gates above, plus exact relocation
and explicit worktree-linking UX, summary-list pagination expectations, and legacy
fork ancestry classification. Do not guess whether an old parent field means
agent ancestry or fork provenance; inspect the originating records.
