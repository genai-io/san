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
  ├── Session  (a resumable conversation; workspace = the checkout it runs in)
  │     ├── events     append-only, authoritative, single writer
  │     ├── attrs      user intent: renamed title, archive, pin (optional)
  │     └── blobs      content referenced by events (images, large tool outputs)
  └── cache            derived summaries only; deleting it is always safe
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
  transcripts/<sid>.attrs.json     user intent (authoritative; absent = defaults)
  cache/summaries/<sid>.json       list-row summary (derived)
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

### Vocabulary

Every type is `<object>.<action>`. Objects name what the event is about; actions
have one meaning each:

| Action | Meaning |
| --- | --- |
| `create` | A new identity. Once per session, always `seq 1`. |
| `update` | A change to named values: a patch (absent / set / clear) for session fields, or `set` / `remove` by name for a catalogue. |
| `append` | Adds to the end of the conversation. |
| `compact` | Replaces the active context with a summary. History keeps everything. |
| `start` / `finish` | A bounded operation. `finish` names its `start` through `source` and carries the outcome. An operation has a durable `start` only when nothing else already marks its beginning. |
| `decide` | Resolves a gate that guards an effect. |
| `add` | A display-only record. |

### Event types

| Group | Event | Payload | Projection effect |
| --- | --- | --- | --- |
| Identity | `session.create` | `project`, `workspace`, `initial_cwd`, `agent_id`, `config` (initial overrides); `parent? {session, call_id?}`, `fork? {session, seq}` | Identity and provenance. |
| | `session.update` | `config?`: provider, model, generation options, mode, autopilot policy. `state?`: generated title, todos, execution_cwd, autopilot progress, group hint | `config` applies to the next request; `state` replaces fields. |
| Conversation | `message.append` | `messages`: nonempty list of native SDK messages | Appends to history and active context. |
| | `context.compact` | `summary`: native message; `through`: seq | Active context becomes `[summary]` + messages appended after `through`. The summary joins history. |
| Model calls | `inference.start` | `turn_id`, `input_through` (seq), `request` (provider, model, options), `instructions_digest`, `toolset_digest` | Opens one model call (each retry is its own call). Output messages name it as `source`. |
| | `inference.finish` | `outcome`, `stop_reason?`, `usage?`, `duration_ms?`, `error? {message, retryable}` | Closes the call named by `source`. |
| Tool calls | `permission.decide` | `call_id`, `decision` (allow/deny/cancelled), `by` (config/mode/user/hook/reviewer), `scope?`, `reason?`, `asked? {options, detail, at}` | Gate result. An `allow` is the point after which the tool may run. Never re-grants on replay. |
| | `tool.finish` | `call_id`, `outcome`, `duration_ms?`, `error?`, `details?` (file change, bash, agent), `child_session?` | Outcome and display details. The result body is the tool-result message. |
| What the model could see | `instructions.update` | `set {name: {content, slot}}`, `remove [name]`, `cause` | System-prompt sections from this point on. |
| | `toolset.update` | `set {name: native tool schema}`, `remove [name]`, `cause` | Tools offered to the model from this point on. |
| | `skills.update` | `set {name: disabled/enabled/active}`, `remove [name]`, `cause` | Skill availability from this point on. |
| Audit and display | `hook.finish` | `hook`, `trigger`, `matcher?`, `outcome` (ran/blocked/error), `duration_ms?`, `reason?` | Record of a completed hook run. |
| | `annotation.add` | `text`, `by_agent?`, `after_message?` | Note shown to the person, never to the model. Placed at its own position unless `after_message` is set. |

Payload rules:

- Native messages keep their SDK type and every provider field (signatures,
  encrypted reasoning, content order, tool IDs). Message IDs are unique within a
  session.
- Patches distinguish absent, set and clear; `false`, `0` and `""` are values.
  Catalogue updates name each entry once across `set` and `remove`. Unknown
  fields are rejected. Empty patches and unchanged catalogues are not written.
- `cause` says who changed a catalogue: `boot`, `mcp:<server>`, `plugin:<name>`,
  `user:<command>`.
- `outcome` is `success`, `failure` or `cancelled`; failures carry a bounded
  error, cancellations a reason.
- Never stored: secrets, token deltas, progress ticks, heartbeats, repeated
  snapshots, or anything derivable (previews, last prompt).

A request is reconstructed from projections, not copied into the event:
context at `input_through`, plus instructions and toolset as projected at the
`inference.start`. The digests verify that reconstruction. Storage stays linear
in conversation length, and catalogue changes keep their timing and cause.

### Commit rules

1. **Effects follow facts.** The assistant message carrying tool calls is
   committed before any tool runs; `inference.start` before the model is called;
   `permission.decide allow` before the tool runs. Every tool call passes the
   gate, including automatic allows. If a commit fails, the effect does not start.
2. **Results commit with their outcome.** A tool-result message and its
   `tool.finish`, or generated messages and `inference.finish`, go in one batch.
   Automatic decisions may share the assistant message's batch.
3. **Unfinished is visible, never invented.** On resume:
   - a call with no decision never ran;
   - a call allowed but without `tool.finish` has an unknown outcome;
   - an `inference.start` without a finish was interrupted.

   None is retried or completed automatically.
4. **Ambiguous commits are resolved by re-reading.** After an error, the writer
   re-reads the tail; if the batch ID is present the commit happened.

### Context operations

The active context changes only through `message.append` and `context.compact`.
Every other operation that seems to change it is covered elsewhere:

| Operation | Recorded as |
| --- | --- |
| User input, model output, tool results, messages delivered from other sessions | `message.append` |
| Compaction: automatic, manual `/compact`, recovery from prompt-too-long | `context.compact` with its `inference.start` / `finish` |
| `/clear` | A new session (`Create`). The old one stays intact and resumable. No `context.clear`: one session holds one conversation. |
| Rewind or edit an earlier message (not implemented today) | `Fork` at the `seq` before that message, then `message.append` in the fork. The source is never rewritten, so no `context.rewind` is needed. |
| Interrupted turn leaving tool calls without results | Nothing. Context keeps them; request building repairs tool pairing (`ai.Repair`) every time. |
| Loading context into the runtime on resume, fork or subagent continuation | Nothing. The runtime receives the projection; a replacement equal to the projection is not an event. Schema 4 records it as `conversation.replaced`. |
| Subagent model fallback | `session.update.config`; context unchanged. |
| Provider adaptation of a request (e.g. images for text-only models) | Nothing. A pure function of context and request config. |

### Projections

A pure projector folds committed events into:

- **Context** — the active message list sent to the model.
- **State** — effective overrides, session state and the three catalogues.
- **History** — every message ever held plus audit and annotations, for display
  and inspection. Pre-compaction messages stay here.
- **Summary** — the list-row data cached in `cache/summaries/`.

Resume is `Open → project → resolve current settings and trust → start runtime`.
TUI, headless and ACP share it; ACP checks the requested cwd against the restored
`execution_cwd` before building services. Catalogues are rebuilt from current
configuration at resume; differences from the projection are written as
`*.update` events with cause `resume`.

## Key flows

### Turn

```text
commit [message.append user]
commit [inference.start]                                --> call model
commit [message.append assistant(tool calls), inference.finish,
        permission.decide (automatic ones)]
  each asked call: commit [permission.decide]           --> run tool if allowed
  each call:       commit [message.append tool result, tool.finish]
```

### Subagents

A subagent is a separate session; the parent records only what it accepted.

```text
parent: assistant tool call C (Agent), permission.decide allow C
child:  session.create { parent: {parent_sid, C} }   committed before the child runs
        ... its own independent events ...
parent: commit [message.append tool result for C, tool.finish { call_id: C, child_session }]
```

- A continuation (`SendMessage` to an existing child) is another tool call with
  its own `tool.finish { child_session }`.
- A result delivered later, outside the tool call (background completion, group
  message), is a `message.append` in the original parent with `source` pointing
  at the child's content event.
- Sessions started without a tool call (cron, workflow) set `parent.call_id`
  only when there is one.
- Delivery and retry belong to the transport or group inbox; the stable batch ID
  makes redelivery idempotent. Replay never recreates processes, callbacks or
  child ownership.

### Fork

Fork at `seq N` of a source session:

1. Under a staging name, write `session.create { fork: {source, N} }` followed by
   a verbatim copy of events `2..N`, and copy the referenced blobs.
2. Clear the group hint; publish by renaming the events file into place.

Positions and native message IDs are unchanged, so no reference is rewritten.
The fork is independent: deleting the source never breaks it, and unfinished
operations in the copied prefix stay unfinished.

### Compaction

```text
commit [inference.start]             capture: context projected at seq S
generate summary of that context     (no lock held; turns are paused)
commit [context.compact { summary, through: S }, inference.finish]

before: context = m1 .. m40  (all at seq <= S)
after:  context = [summary]            history = m1 .. m40, summary
```

`through` names exactly what the summary covers. The inspector can show which
messages were summarized, and anything appended after `S` stays in context
rather than being lost; today nothing is, because input queues during
compaction. Recent requests the summary must keep are written into the summary
text. A failed or cancelled compaction writes only `inference.finish` and leaves
context unchanged.

## Consistency and recovery

| Concern | Mechanism |
| --- | --- |
| Atomic groups | One sdk-go batch per group: one line, checksummed, hash-chained to the expected tail. |
| Durability | `Commit` returns after file sync. Local filesystems only; directory sync and power-loss certification are not claimed. |
| Single writer | sdk-go per-session `flock`, held by an open file and released on process death. A second writer gets `ErrBusy`. |
| Crash recovery | The next writer cuts an unterminated last line. A corrupt complete line is an error, never skipped. A record type newer than this binary blocks resume. |
| Assets | Blobs are written and synced before the event that references them. |
| Create / Fork | Create is one batch, so a crash leaves either nothing or a valid session. Fork publishes by rename. |
| Caches | A summary records the events file's size and last batch hash when built; it is valid only while both match. Appends change the size and replacement changes the hash; mtime is not used. Cache writes are atomic replaces; a stale or missing cache is rebuilt, never trusted. |
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
  `Append` of `context.compact` with `inference.finish`. `session.create` is
  reserved for `Create` and `Fork`.
- `SetAttrs` changes the user title, archive and pin without the writer lock, so a picker
  can organize a session another process has open.
- Errors are distinguishable: not found, busy, closed, conflict, invalid,
  unsupported version, corrupt, cancelled, storage.

The current `Store` (`Create/Open/List/Fork/Delete`, `Append(transcript.Record)`,
immediate deletion) evolves into this surface; consumers keep narrow local
interfaces, and generic pieces move into sdk-go.

## Lifecycle and retention

Two kinds of session data exist, and they are stored apart:

| | `transcripts/<sid>.attrs.json` | `cache/summaries/<sid>.json` |
| --- | --- | --- |
| Holds | User intent: `title` (renamed), `archived_at`, `pinned` | Projection of events for the list: title, last prompt, counts, workspace, parent, last activity |
| Derivable from events | No | Yes |
| If lost | Data loss | Slower until rebuilt |
| Writer | Anyone, under a short per-session lock; no writer lock | Any reader, on rebuild |

Keeping them apart means a cache bug can never damage user intent and
`cache/` can always be deleted. Most sessions never get an attrs file; absent
means defaults. The displayed title is `attrs.title` if set, otherwise the
generated `state.title` from events, so a fork inherits the generated title.
Last activity is the last event's `time`; opening, reading, scanning and
restoring do not count.

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
  deleted_at), then renames the events file, attrs and blobs into `trash/<sid>/`
  and drops the cached summary. On
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
| SDK truncate + re-saved messages (`conversation.replaced`) | With a `session.compacted` naming the summary: `context.compact`. Into an empty context (subagent records): `message.append`. Equal to the current context (resume reload): dropped. Anything else blocks conversion. |
| `session.state.patched` | `session.update` (model/mode/autopilot → config; title/tasks/group → state); `lastPrompt` dropped as derivable |
| `inference.requested` / `responded` / `failed` | `inference.start` / `inference.finish` |
| `system.section.added/removed` | `instructions.update` set/remove; `caller` → `cause` |
| `tool.added/removed` | `toolset.update` set/remove; `caller` → `cause` |
| `tool.completed` | `tool.finish` |
| `permission.required` + `permission.decided` | One `permission.decide`; the request's options and detail become `asked`. A request without a decision is dropped (the call never ran). |
| `hook.fired` | `hook.finish` when its outcome is terminal; async-only dropped |
| `skill.state.changed` | `skills.update` |
| `notice.added` | `annotation.add` (`afterMessageId` → `after_message`) |

## Alternatives considered

- **Globally unique event IDs** (previous draft). They require allocation,
  cross-partition collision checks and rewriting every local reference on fork.
  Positional `(session, seq)` identity plus batch IDs for idempotency gives the
  same guarantees with none of that.
- **`tool.start`, `delegation.start/finish`, `permission.request`** (previous
  draft). Each duplicates a fact another event already fixes: the assistant
  message records the call, `permission.decide allow` marks the point after
  which it may run, the child's `session.create` and `tool.finish.child_session`
  record delegation, and `permission.decide.asked` keeps what the person was
  shown. Every scenario they covered maps to the commit rules above.
- **Prompt and tool definitions as blobs per request.** Self-describing, but it
  loses when and why a catalogue changed (an MCP server connecting, a skill
  being enabled). Delta events keep the cause and stay linear in size; the
  digests on `inference.start` still verify each request.
- **Archive and pin as events.** That would require the writer lock to organize
  a session open elsewhere; they are not conversation facts.
- **A generic `context.replace`, or `context.clear` / `context.rewind`.** San's
  only in-place rewrite is compaction; `/clear` creates a new session, and a
  rewind or edit is better as a fork that leaves the source intact (see
  [Context operations](#context-operations)). One concrete event is easier to
  validate and read.
- **attrs and summary cache in one file.** Rebuilding the cache would rewrite
  user intent, so "deleting caches is safe" would no longer hold.
- **`last_resumed_at` as activity.** Opening without doing anything is not
  activity; the last event time is enough and needs no extra authority.
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
- Compaction: `through` boundary, consecutive compactions, failure leaves context unchanged.
- Requests: context and catalogues projected at each `inference.start` match its
  digests; resume reload and request-time repair write no events.
- Interrupted tools: no decision → never ran; allowed without finish → unknown
  outcome; neither is retried.
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
