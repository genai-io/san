# PROP-0005: Session management and file-backed persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-08. Defines the target model for projects, sessions and their
events on top of sdk-go's JSONL store. Storage stays file-backed; SQLite
is out of scope (see [Alternatives](#alternatives-considered)).

| Piece | Status |
| --- | --- |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | Merged: committed batches (`Writer.Commit`, `Store.Batches`). |
| [#737](https://github.com/genai-io/san/pull/737) | Open: project/worktree identity, checkout-local settings, typed events behind a private writer. Runtime still writes schema 4. |
| Runtime cutover, lifecycle (trash/retention), legacy conversion | Not started; specified below. |

## Model

```text
Project  (one per Git repository, or per directory outside Git)
  └── Session  (a resumable conversation; workspace = the checkout it runs in)
        ├── events         append-only, authoritative, single writer
        ├── attrs          small mutable file: title, archive, pin, activity
        ├── blobs          content referenced by events (images, large outputs, prompts)
        └── summary cache  rebuildable
```

Four invariants carry the whole design:

1. **Events are the only source of truth for a conversation.** Messages,
   context, settings overrides and audit are all projections of it. There is no
   parallel messages/state store.
2. **One writer per session, across processes.** Readers never need the writer.
3. **Facts before effects.** Anything that precedes or authorizes a side effect
   (a model call, a tool run) is committed before the effect starts. Replay only
   projects; it never calls models, tools or hooks.
4. **Everything derived is disposable.** Summaries, indexes and pages can be
   deleted and rebuilt from events without loss.

A session is a conversation, not a process: `Close` releases the handle and the
session stays resumable. There is no "session ended" event.

## Project identity and configuration

| Startup location | Project key | Workspace |
| --- | --- | --- |
| Inside Git | Canonical absolute Git common directory. Main checkout, linked worktrees and subdirectories share one project; separate clones and submodules are distinct. | Checkout root (`git rev-parse --show-toplevel`) |
| Outside Git | Exact canonical startup directory; no ancestor search. | Same directory |

Paths are absolute, symlink-resolved and case-preserved, using the same
normalization as trust checks. The project directory is
`~/.san/projects/<hash(kind + key)>/`. `project.json` inside it records the key
and a display name; it is descriptive, not an identity registry. A moved
repository therefore gets a new project; relinking an old one is an explicit
command, never automatic. Legacy `<hash(cwd)>` partitions stay readable where
they are; nothing is moved or merged.

Each session fixes `project`, `workspace` and `initial_cwd` at creation. The
current working directory is session state (`execution_cwd`); changing it
refreshes tools, settings and trust for the new location but never moves the
session to another project.

Configuration does not move into session storage:

- Project settings and instructions load from the **current checkout**, so they
  follow its branch. User-global settings are shared naturally. There is no
  shared project root and no fallback to the main checkout.
- Existing loaders, precedence (user → project → local → persona) and
  provenance are unchanged ([configuration](../reference/configuration.md)).
- A session stores only its own overrides (model, mode, …). On resume they are
  re-resolved against current settings and credentials; permission grants are
  never restored from history. A model that no longer exists is an error the
  user resolves explicitly.

## Storage layout

```text
~/.san/projects/<project>/
  project.json                     key, display name
  transcripts/<sid>.jsonl          events (sdk-go batch mode)
  transcripts/<sid>.jsonl.lock     writer lock (sdk-go)
  transcripts/<sid>.attrs.json     mutable attributes (authoritative)
  transcripts/<sid>.jsonl.meta     summary cache (rebuildable)
  blobs/<sid>/                     content-addressed by sha256
  trash/<sid>/                     deleted sessions: manifest + moved files
  legacy/<sid>.jsonl               schema-4 originals kept after conversion
```

Parent and child sessions are peers in `transcripts/`; the link lives in the
child's `session.create`. Files whose names start with `.` are staging and are
ignored by discovery.

## Events

### Envelope

Each event is one sdk-go application record. A group of events written together
is one sdk-go batch: a single checksummed JSONL line, chained to the previous
tail, so readers see all of it or none of it.

| Field | Meaning |
| --- | --- |
| `seq` | 1-based position of the event in the session. Assigned by the writer from the current tail; it is the event's identity. |
| `type` | `<object>.<action>`, from the table below. |
| `v` | Payload schema version for this type. |
| `time` | UTC Unix milliseconds. Informational; ordering is `seq`. |
| `source?` | Reference to the event this one derives from: `{seq}` in the same session, `{session, seq}` in another. Provenance only. |
| `payload` | Typed JSON for `type`. |

Events are identified by position rather than by allocated IDs. Committed
prefixes are immutable, so `(session, seq)` is stable for the session's lifetime and
a fork that copies a prefix keeps every in-session reference valid unchanged.
Retry idempotency comes from the batch: a retried commit reuses its batch ID,
and sdk-go treats the same ID with the same content as already committed and a
changed one as a conflict.

### Event types

| Event | Payload | Projection effect |
| --- | --- | --- |
| `session.create` | `project`, `workspace`, `initial_cwd`, `agent_id`, `config` (initial overrides); `parent? {session, call_id}`, `fork? {session, seq}` | Always `seq 1`. Identity and provenance. |
| `message.append` | `messages`: nonempty list of native SDK messages | Appends to history and active context. |
| `context.replace` | `reason` (compaction/clear/rewind/edit); `through`: seq; `entries`: ordered list, each an existing message ID or a new native message | Active context becomes `entries` + every message appended after `through`. New messages (e.g. a summary) join history; nothing leaves it. |
| `session.update` | `config?`: model, provider, generation options, mode, autopilot policy; `state?`: todos, execution_cwd, autopilot progress, group hint, active skills | Next requests use the new overrides; state is replaced field by field. |
| `inference.start` | `turn_id`, `input_through` (seq), `request` (provider, model, options), `instructions` and `tools` (blob digests) | Opens an attempt. The output messages name it as `source`. |
| `inference.finish` | `outcome`, `stop_reason?`, `usage?`, `duration_ms?`, `error?` | Closes the attempt named by `source`. |
| `tool.finish` | `call_id`, `outcome`, `duration_ms?`, `error?`, `details?`, `child_session?` | Audit and display details for one tool call. The result body is the tool-result message. |
| `permission.decide` | `call_id`, `decision` (allow/deny/cancelled), `scope`, `by` (config/user/hook/reviewer), `asked_at?`, `reason?` | Audit only; never re-grants on replay. |
| `hook.finish` | `hook`, `trigger`, `outcome`, `duration_ms?`, `effects?` | Audit of a completed hook. Async dispatch is not recorded until it completes. |
| `annotation.add` | `text`, `anchor?` (seq) | Display-only note; never model context. |

Payload rules:

- Native messages keep their SDK type and every provider field (signatures,
  encrypted reasoning, content order, tool IDs). Message IDs are unique within a
  session.
- Patches distinguish absent, set and clear; `false`, `0` and `""` are values.
  Unknown fields are rejected. Empty patches are not written.
- `outcome` is `success`, `failure` or `cancelled`; failures carry a bounded
  error, cancellations a reason.
- Never stored: secrets, token deltas, progress ticks, heartbeats, repeated
  snapshots, or anything a renderer can derive (previews, last prompt).

`inference.start` points at its input by `input_through` instead of listing
message IDs, so storage stays linear in conversation length. System prompts and
tool definitions are stored once per distinct content as blobs and referenced by
digest, which makes every request reconstructible without separate catalogue
events.

### Commit rules

1. **Effects follow facts.** The assistant message carrying tool calls is
   committed before any tool runs; `inference.start` before the model is called;
   `permission.decide` before an allowed tool runs. If the commit fails, the
   effect does not start.
2. **Results commit with their outcome.** A tool-result message and its
   `tool.finish`, or generated messages and `inference.finish`, go in one batch.
3. **Unfinished is visible, never invented.** A tool call without a result, or an
   `inference.start` without a finish, is reported as interrupted on resume. It
   is not retried or completed automatically.
4. **Ambiguous commits are resolved by re-reading.** After an error, the writer
   re-reads the tail; if the batch ID is present the commit happened.

### Projections

A pure projector folds committed events into:

- **Context** — the active message list sent to the model.
- **State** — effective overrides and session state.
- **History** — every message ever held plus audit and annotations, for display
  and inspection. Pre-compaction messages stay here.
- **Summary** — the list-row data cached in `.meta`.

Resume is `Open → project → resolve current settings and trust → start runtime`.
TUI, headless and ACP share it; ACP checks the requested cwd against the restored
`execution_cwd` before building services.

## Key flows

### Turn

```text
commit [message.append user]
commit [inference.start]                       --> call model
commit [message.append assistant(tool calls), inference.finish]
  for each call: commit [permission.decide]    --> run tool
                 commit [message.append tool result, tool.finish]
```

### Subagents

A subagent is a separate session; the parent records only what it accepted.

```text
parent: assistant tool call C (Agent)
child:  session.create { parent: {parent_sid, C} }   committed before the child runs
        ... its own independent events ...
parent: commit [message.append tool result for C, tool.finish { call_id: C, child_session }]
```

A later result delivered outside the tool call (background completion, group
message) is a `message.append` in the original parent, with `source` pointing at
the child's content event. Delivery and retry belong to the transport or group
inbox; the stable batch ID makes redelivery idempotent. Replay never recreates
processes, callbacks or child ownership.

### Fork

Fork at `seq N` of a source session:

1. Under a staging name, write `session.create { fork: {source, N} }` followed by
   a verbatim copy of events `2..N`, and copy the referenced blobs.
2. Clear the group hint; publish by renaming the events file into place.

Positions and native message IDs are unchanged, so no reference is rewritten.
The fork is independent: deleting the source never breaks it, and unfinished
attempts in the copied prefix stay unfinished.

### Compaction and other context rewrites

Every change to the active context other than appending — compaction, `/clear`,
rewind, clearing old tool output — is one `context.replace`.

```text
capture context at seq S --> generate summary (outside any lock)
commit [context.replace { reason: compaction, through: S,
                          entries: [summary, retained IDs...] }, inference.finish]
```

Anything committed after `S` while the summary was generated stays in context
after the entries, so a summary is never stale and never needs rejection.
Entries may reference only messages in the context at `S`, and the resulting
context must keep every tool call together with its result. A failed or
cancelled compaction writes only `inference.finish` and leaves context
unchanged. Retained messages are referenced, not copied, so compaction does not
duplicate them.

## Consistency and recovery

| Concern | Mechanism |
| --- | --- |
| Atomic groups | One sdk-go batch per group: one line, checksummed, hash-chained to the expected tail. |
| Durability | `Commit` returns after file sync. Local filesystems only; directory sync and power-loss certification are not claimed. |
| Single writer | sdk-go per-session `flock`, held by an open file and released on process death. A second writer gets `ErrBusy`. |
| Crash recovery | The next writer cuts an unterminated last line. A corrupt complete line is an error, never skipped. A record type newer than this binary blocks resume. |
| Assets | Blobs are written and synced before the event that references them. |
| Create / Fork | Create is one batch, so a crash leaves either nothing or a valid session. Fork publishes by rename. |
| Caches | `.meta` stores the tail (`count`, `hash`) it was built from and is valid only if it matches the events file. Cache writes are atomic replaces. A stale or missing cache is rebuilt, never trusted. |
| Event pages | Cursor = `(seq, byte offset, tail hash at that offset)`. A page resumes reading at the offset, so it costs O(page) rather than a replay of the prefix; a hash mismatch means the events file was replaced, and the cursor fails. |

A consistent backup copies the project directory while no writer is active.

## API

```go
func OpenStore(ctx context.Context, startDir string) (*Store, error)

func (s *Store) Create(ctx context.Context, opts CreateOptions) (*Session, error)
func (s *Store) Open(ctx context.Context, id string) (*Session, error)
func (s *Store) Fork(ctx context.Context, id string, opts ForkOptions) (*Session, error)
func (s *Store) Get(ctx context.Context, id string) (Summary, error)
func (s *Store) List(ctx context.Context, q SessionQuery) (SessionPage, error)
func (s *Store) Events(ctx context.Context, id string, q EventQuery) (EventPage, error)
func (s *Store) SetAttrs(ctx context.Context, id string, patch AttrsPatch) error
func (s *Store) Delete(ctx context.Context, id string) error
func (s *Store) Restore(ctx context.Context, id string) error
func (s *Store) Clean(ctx context.Context, opts CleanOptions) (CleanResult, error)
func (s *Store) Close() error

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error // one batch
func (s *Session) Snapshot() Projection                              // in-memory, committed only
func (s *Session) Close() error                                      // drain, release lock
```

- `OpenStore` resolves the project from `startDir`; every ID must belong to it.
  A cross-project fork takes the source from its own `Store`.
- `Create`, `Open` and `Fork` take the writer lock before loading. `Open` keeps
  the stored workspace and cwd.
- `Get`, `List` and `Events` never take the writer lock. `List` spans all
  workspaces of the project and filters by workspace, parent, archived,
  activity (`recent` | `all`) and visibility (`live` | `trash` | `all`), ordered
  by `(last_active, id)` with an opaque cursor.
- `Append` is the only write path for conversation events; compaction is an
  `Append` of `context.replace` with `inference.finish`. `session.create` is
  reserved for `Create` and `Fork`.
- `SetAttrs` changes title, archive and pin without the writer lock, so a picker
  can organize a session another process has open.
- Errors are distinguishable: not found, busy, closed, conflict, invalid,
  unsupported version, corrupt, cancelled, storage.

The current `Store` (`Create/Open/List/Fork/Delete`, `Append(transcript.Record)`,
immediate deletion) evolves into this surface; consumers keep narrow local
interfaces, and generic pieces move into sdk-go.

## Lifecycle and retention

Attributes that organize sessions are not conversation facts, so they live in
`<sid>.attrs.json` rather than in events: `title`, `archived_at`, `pinned`,
`last_resumed_at`. The file is replaced atomically under a short per-session
lock. Last activity is the later of the last event's `time` and
`last_resumed_at`; reads, scans, cache rebuilds and restores do not count.

| Policy | Default |
| --- | --- |
| Recent list | Shows sessions active in the last 30 days; display filter only. Pinned sessions always show. |
| Archive | Hidden from the default list; fully readable and resumable. |
| Pin | Blocks Delete and automatic retention until unpinned. |
| Delete | Moves to trash. Kept 7 days, then purged. |
| Automatic retention | Off. When enabled (e.g. 90 days inactive), moves eligible sessions to trash; it never purges directly. |

```text
live/archived --Delete / retention--> trash --7 days--> purged
      ^                                 |
      +-------------- Restore ----------+
```

- **Delete** requires the writer lock (an open session cannot be deleted) and a
  non-pinned session. It writes `trash/<sid>/manifest.json` first (files,
  deleted_at), then renames the events file, attrs and blobs into `trash/<sid>/`. On
  startup, a manifest with unmoved files is rolled forward. Children and forks
  are independent sessions and are not touched.
- **Restore** reverses the moves. It fails if the ID already exists live; it
  never overwrites or renames.
- **Purge** removes trash directories past their deadline, manifest last.
- **Clean** runs retention and purge, bounded per run, after the UI is
  interactive. `DryRun` reports what would happen without writing. Automatic
  retention acts only on the current workspace unless configured project-wide.
- **GC** removes staging files and unreferenced blobs older than 24 hours.
  Anything it cannot attribute is kept.

Reads of trashed sessions require `visibility=trash`; `Open`, `Fork` and
`Append` reject them until restored.

## Migration from schema 4

New sessions use typed events. Schema-4 transcripts remain readable through an
adapter and are converted on first resume, under the writer lock:

1. Map records with the table below into a staging events file, copying blobs.
2. Verify the projected context and history match the legacy reader.
3. Move the original to `legacy/<sid>.jsonl` and publish the new events under the
   same session ID, so parent links and group membership stay valid.

If verification fails, the session stays read-only in legacy form and can be
continued as a fork of its legacy context. Conversion never invents facts:
fields the old records lack are left absent.

| Schema 4 | Target |
| --- | --- |
| `session.started`, `session.forked` | `session.create` |
| SDK message / `message.appended` | `message.append` |
| SDK truncate + re-saved messages (`conversation.replaced`) | `context.replace`; re-saved messages that match an earlier message become ID entries, others are new entries. `reason: compaction` when a `session.compacted` names the summary, otherwise `edit`. |
| `session.state.patched` | `session.update` (model/mode/autopilot → config; tasks/group → state); title → attrs |
| `inference.requested` / `responded` / `failed` | `inference.start` / `inference.finish` |
| `system.section.*`, `tool.added/removed` | Folded into instruction/tool blobs of the next `inference.start` |
| `tool.completed` | `tool.finish` |
| `permission.required` + `permission.decided` | `permission.decide` (`asked_at` from the request) |
| `hook.fired` | `hook.finish` when its outcome is terminal; async-only dropped |
| `skill.state.changed` | `session.update.state.active_skills` |
| `notice.added` | `annotation.add` |

## Alternatives considered

- **Globally unique event IDs** (previous draft). They require allocation,
  cross-partition collision checks and rewriting every local reference on fork.
  Positional `(session, seq)` identity plus batch IDs for idempotency gives the
  same guarantees with none of that.
- **Start events for tools, delegation and permission requests.** The committed
  assistant message already records the call before it runs, the child's
  `session.create` records delegation, and a call without a decision is a
  pending request. Separate start events duplicate those facts.
- **Catalogue events for instructions, tools and skills.** Content-addressed
  blobs referenced from `inference.start` make each request self-describing and
  remove the need to replay catalogues to resolve a digest.
- **Archive and pin as events.** That would require the writer lock to organize
  a session open elsewhere; they are not conversation facts.
- **Compaction rejected when the tail moved.** Folding post-capture messages into
  the result makes staleness impossible instead of detected.
- **Random project IDs with a registry.** A registry needs locked updates and
  reconciliation, and a moved repository still needs an explicit relink. A
  derived key needs neither.
- **SQLite.** Measured against JSONL on 2026-11-07 (Apple M5, durable sync on
  both sides): appends were equal (≈4 ms), replay of 1,000 messages was ~20%
  slower, listing 100 sessions was 3.5× faster with warm caches but 25× slower
  once the in-memory cache was lost (JSONL had persisted summaries), and the
  binary grew 3.8 MB. Not worth a second storage engine; revisit only with new
  evidence, after optimizing summary caching and replay allocations.
- **In-place migration or dual writes.** sdk-go forbids mixing legacy and batch
  records in one file, and dual writes double the failure modes. Convert once,
  keep the original.

## Validation

- Identity: worktrees share a project; clones, submodules and non-Git
  subdirectories do not; config stays checkout-local.
- Events: every event type round-trips; native provider fields survive; replay has
  no side effects; batch retries are idempotent and conflicts fail.
- Crash injection at every write/sync/rename: partial batch, mid-fork, mid-delete,
  mid-restore, mid-conversion. Each recovers to a committed state.
- Compaction with concurrent appends; consecutive compactions; tool-pair integrity.
- Fork independence after source deletion; subagent late results.
- Lifecycle: pin and busy guards, trash deadline, retention off by default, GC
  keeps unknown files.
- Legacy: conversion parity on real schema-4 sessions, including compactions and
  replaced conversations.

## Open questions

- Exact payload Go types, and which parts (envelope, projector, batch helpers)
  belong in sdk-go.
- `relink` UX for moved repositories.
- Whether the summary cache should become one per-project index once listing
  large projects is measured.
