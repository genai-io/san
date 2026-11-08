# PROP-0005: Session management and file-backed persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-08. Defines the target model for projects, sessions and their
events on top of sdk-go's JSONL store. Storage stays file-backed; SQLite
is out of scope (see [Alternatives](#alternatives-considered)).

| Piece | Status |
| --- | --- |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | Merged: committed batches (`Writer.Commit`, `Store.Batches`). |
| [#737](https://github.com/genai-io/san/pull/737) | Merged: project/worktree identity, checkout-local settings, typed events behind a private writer, using the previous draft's 18-type model. It converges to this design before any runtime cutover. Runtime still writes schema 4. |
| [#738](https://github.com/genai-io/san/pull/738) | Merged: optional SDK admission boundaries in core. |
| Runtime cutover, lifecycle (trash/retention), legacy conversion | Not started; specified below. |

## Model

```text
Project  (one per Git repository, or per directory outside Git)
  ├── Session  (a resumable conversation; workspace = the checkout it runs in)
  │     ├── events     append-only, authoritative, single writer
  │     ├── attrs      user intent: renamed title, archive, pin (optional)
  │     └── blobs      content referenced by events (images, large tool outputs, standalone requests)
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
  sessions/<sid>/                  everything authoritative about one session
    events.jsonl                   events (sdk-go batch mode)
    events.jsonl.lock              writer lock (sdk-go)
    attrs.json                     user intent (absent = defaults)
    blobs/                         content-addressed by sha256
  cache/<sid>.json       list-row summary (derived)
  trash/<sid>/                     a deleted session directory, moved whole
  transcripts/<sid>.jsonl          schema-4 sessions, read-only once converted
```

Directories group data by what happens when it is lost. `sessions/` and
`trash/` hold authoritative data, `cache/` is disposable, and `transcripts/` is
the legacy format, left untouched. A session is one directory, so deleting,
restoring, forking and backing it up each handle one unit, and a rename of that
directory is atomic. A session in `sessions/` shadows a same-ID file in
`transcripts/`. Parent and child sessions are peers in `sessions/`; the link
lives in the child's `session.create`. Names starting with `.` are staging and
are ignored by discovery.

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
| Model calls | `inference.start` | `purpose`, `turn_id?`, `request` (provider, model, options), `input` (`conversation {append?}` or `standalone`), `instructions_digest?`, `toolset_digest?` | Opens one model call; each retry is its own call. See [Model calls](#model-calls). |
| | `inference.finish` | `outcome`, `stop_reason?`, `usage?`, `duration_ms?`, `error? {message, retryable}` | Closes the call named by `source`. |
| Tool calls | `permission.decide` | `call_id`, `decision` (allow/deny/cancelled), `by` (config/mode/grant/user/hook/reviewer), `scope?`, `reason?`, `asked? {options, detail, at}` | Gate result. An `allow` is the point after which the tool may run. `grant` means an earlier session-scoped approval; grants are never restored on resume, so such calls ask again. |
| | `tool.finish` | `call_id`, `outcome`, `duration_ms?`, `error?`, `details?` (file change, bash, agent), `child_session?` | Outcome and display details of a call that ran. The result body is the tool-result message. A call that did not pass the gate has only its decision and an error tool-result message. |
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
- An unknown `type`, or a `v` newer than this binary supports, blocks resume and
  writes. The session stays readable for display, with such events shown as
  opaque.

### Model calls

San records every model call it makes, so usage and cost are complete and
every request can be rebuilt. A call is `inference.start` plus
`inference.finish`. Its result goes into the event that owns it, and that
event names the call as `source`.

To rebuild a request, the event must say what the model was sent. Calls fall
into two kinds:

| Kind | What the model was sent | What `inference.start` records | Example |
| --- | --- | --- | --- |
| Conversation call | This session's own conversation at that moment (context, system prompt, tools), plus a few messages added for this call only | `input.conversation {append?}`: just the added messages. The rest is already in the events; `instructions_digest` and `toolset_digest` check it. | A normal turn appends nothing. `/btw` appends the question. |
| Standalone call | A request built separately, with its own prompt and inputs | `input.standalone`: digest of an exact copy of the request, stored once in `blobs/` | The reviewer is sent its own prompt plus the tool call under review. |

The boundary is mechanical. A call is a conversation call only if all three
hold:

1. its messages start with the session's context exactly as projected, and
   anything added for the call comes after it, never inserted or edited;
2. its system prompt and tools match the projected instructions and toolset,
   checked by digest when `inference.start` is committed;
3. it goes through the same request-time adaptation as a turn (tool-pairing
   repair, provider adaptation) and nothing else.

Anything else is standalone: summarizing text built from the conversation, a
different prompt, a subset of tools, or a context not yet recorded. Provider,
model and options never decide the kind; both kinds record them in `request`.
The kind follows from the request, not from the purpose: the table below lists
each purpose's usual kind, and a call whose digests do not match at commit is
recorded as standalone. For example, a side call made before the runtime has
started builds its own system prompt, so it is standalone.

Conversation calls are frequent and carry the whole conversation, so copying
each one would store the conversation again on every call; referring to the
events stores it once. Standalone calls are small or rare and their shape
belongs to the code that built them, so an exact copy is the only way to
rebuild them after that code changes.

A conversation call reads the conversation and commits its `inference.start`
in one critical section, so no message can be appended in between. Side calls
send the tools only so the prompt cache matches; the tools never run.

| Purpose | Usual kind | Messages appended | Result recorded as |
| --- | --- | --- | --- |
| `turn` | Conversation | None | `message.append` |
| `btw` (side question) | Conversation | Placeholders for in-flight work, the question | `annotation.add` |
| `recap` | Conversation | Placeholders, the recap prompt | `annotation.add` |
| `suggest` | Conversation | Placeholders, the suggestion prompt | Nothing; accepted text later arrives as user input |
| `compaction` | Standalone | — | `context.compact` |
| `reviewer` | Standalone | — | `permission.decide {by: reviewer}` |
| `hook` (prompt hooks) | Standalone | — | `hook.finish` |
| `autopilot` | Standalone | — | `session.update` (progress) or `message.append` (next instruction) |
| `group` (interpreting `/group`) | Standalone | — | `session.update` (group hint) or `annotation.add` |

A new purpose declares its kind and result event before it ships; standalone
is the default. Agents that run their own loop (subagents, self-learning
review) record in their own sessions.

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
   - an `inference.start` without a finish was interrupted; partially streamed
     output is never committed.

   None is retried or completed automatically.
4. **Ambiguous commits are resolved by re-reading.** After an error, the writer
   re-reads the tail; if the batch ID is present the commit happened.

### Context operations

The active context changes only through `message.append` and `context.compact`.
Every other operation that seems to change it is covered elsewhere:

| Operation | Recorded as |
| --- | --- |
| User input, model output, tool results, messages delivered from other sessions | `message.append` |
| Input queued while a turn or compaction runs | Nothing until it is delivered; then `message.append`. |
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
- **Summary** — the list-row data cached in `cache/`.

Resume is `Open → project → resolve current settings and trust → start runtime`.
TUI, headless and ACP share it; ACP checks the requested cwd against the restored
`execution_cwd` before building services. Catalogues are rebuilt from current
configuration at resume; differences from the projection are written as
`*.update` events with cause `resume`.

## Key flows

### Turn

```text
commit [message.append user]
commit [inference.start {purpose: turn}]                --> call model
commit [message.append assistant(tool calls), inference.finish,
        permission.decide (automatic ones)]
  each asked call:   commit [permission.decide]         --> run tool if allowed
  each allowed call: commit [message.append tool result, tool.finish]
  each denied call:  commit [message.append error tool result]
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

1. Under a staging directory, write `session.create { fork: {source, N} }` followed by
   a verbatim copy of events `2..N`, and copy the referenced blobs.
2. Clear the group hint; publish by renaming the staging directory into `sessions/`.

Positions and native message IDs are unchanged, so no reference is rewritten.
The fork is independent: deleting the source never breaks it, and unfinished
operations in the copied prefix stay unfinished.

### Compaction

```text
commit [inference.start {compaction, standalone}] capture: context at seq S
generate summary of that context                 (no lock; turns are paused)
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

| | `sessions/<sid>/attrs.json` | `cache/<sid>.json` |
| --- | --- | --- |
| Holds | User intent: `title` (renamed), `archived_at`, `pinned` | Projection of events for the list: title, last prompt, counts, workspace, parent, last activity |
| Derivable from events | No | Yes |
| If lost | Data loss | Slower until rebuilt |
| Writer | Anyone, under a short per-session lock; no writer lock | Any reader, on rebuild |

Keeping them apart means a cache bug can never damage user intent and
`cache/` can always be deleted. Most sessions never get an attrs file; absent
means defaults. The displayed title is `attrs.title` if set, otherwise the
generated `state.title` from events, so a fork inherits the generated title.
Last activity is the `time` of the last `message.append`. Catalogue, state and
annotation events, opening, reading, scanning and restoring do not count.

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
  non-pinned session. It writes `deleted.json` (`deleted_at`) into the session
  directory, renames the directory to `trash/<sid>/` in one step, and drops the
  cached summary. Children and forks are independent sessions and are not
  touched. A crash leaves the session either live or trashed, never split.
- **Restore** renames the directory back. It fails if the ID already exists
  live; it never overwrites or renames.
- **Purge** renames a directory past its deadline to `trash/.purging-<sid>/` and
  then removes it; a leftover `.purging-` directory is finished on the next run.
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

1. Map records with the table below into a staging directory `sessions/.<sid>/`, copying blobs.
2. Verify the projected context and history match the legacy reader.
3. Publish by renaming the staging directory to `sessions/<sid>/`, under the same
   session ID so parent links and group membership stay valid. The original
   stays in `transcripts/`, untouched, and is shadowed by the new directory.

If verification fails, the session stays read-only in legacy form and can be
continued as a fork of its legacy context. Conversion never invents facts:
fields the old records lack are left absent.

| Schema 4 | Target |
| --- | --- |
| `session.started`, `session.forked` | `session.create` |
| SDK message / `message.appended` | `message.append` |
| SDK truncate + re-saved messages (`conversation.replaced`) | With a `session.compacted` naming the summary: `context.compact`. Into an empty context (subagent records): `message.append`. Equal to the current context (resume reload): dropped. Anything else blocks conversion. |
| `session.state.patched` | `session.update` (model/mode/autopilot → config; title/tasks/group → state); `lastPrompt` dropped as derivable |
| `inference.requested` / `responded` / `failed` | `inference.start {purpose: turn}` / `inference.finish`; `messageIds` dropped (the context before the event) |
| `system.section.added/removed` | `instructions.update` set/remove; `caller` → `cause` |
| `tool.added/removed` | `toolset.update` set/remove; `caller` → `cause` |
| `tool.completed` | `tool.finish` |
| `permission.required` + `permission.decided` | One `permission.decide`; the request's options and detail become `asked`. A request without a decision is dropped (the call never ran). |
| `hook.fired` | `hook.finish` when its outcome is terminal; async-only dropped |
| `skill.state.changed` | `skills.update` |
| `notice.added` | `annotation.add` (`afterMessageId` → `after_message`) |

## Delivery plan

Merged code (#737, #738) implements the previous draft. Each slice below is an
independent PR that keeps runtime writing schema 4 until the last one.

| # | Slice | Replaces / adds |
| --- | --- | --- |
| 1 | Event model | 18 types, global event IDs and `source_id` → 13 types, `seq` identity, `source` refs, vocabulary, catalogue and `context.compact` payloads |
| 2 | Validation and projector | Reference checks by position; gate, unfinished-call and version rules; Context / State / History projections |
| 3 | Model calls | `purpose`, conversation/standalone input, request blobs, digest check at commit |
| 4 | Attrs and cache | `attrs.json`, `cache/`, size + batch-hash validity, event pages by offset |
| 5 | Lifecycle | `SetAttrs`, `Delete` / `Restore` / `Clean`, whole-directory trash, retention, GC |
| 6 | Runtime cutover | `sessions/` layout; producers write events; fork by prefix copy; resume and ACP checks; schema-4 conversion with parity gate |

Slices 1–2 are gated by #737's tests being ported, not deleted.

Slice 6 changes paths, not only formats. Every consumer of the old flat path moves
to the session directory: `Session.TranscriptPath` and its hook-facing path,
discovery and listing in `session`, the inspector (`cmd/san/inspector.go`,
`internal/inspector`), and `transcript.Store`. Each session directory is opened as
its own sdk-go store with the fixed ID `events`, which keeps sdk-go's batch format,
writer lock and recovery unchanged (verified: one writer per directory, and the
directory renames cleanly once closed). Discovery checks `sessions/<sid>/` first and
falls back to `transcripts/<sid>.jsonl`; a session never appears twice. All of
`sessions/`, `trash/` and `cache/` must be on one filesystem, which is what makes
the renames atomic.

Code layout follows the same split. `internal/session` owns the product Store
and Session; its typed-event code is grouped by responsibility in files
(`event_model`, `event_validation`, `event_projector`, `event_storage`,
`event_query`) rather than a new package, as the event model has no consumer
outside `session`. `internal/transcript` keeps schema 4 and the sdk-go wrapper,
and shrinks to the legacy reader once slice 6 lands. Generic pieces (batch
framing, writer lock) stay in sdk-go.

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
- **An exact copy of every request.** Exact, but each turn's copy repeats the
  whole conversation (quadratic storage) and loses when and why a catalogue
  changed. Copies are kept only for standalone calls.
- **Recording only main-loop calls** (schema 4). Usage and cost would miss
  compaction, reviewer, hook and side calls, and their results would have no
  provenance.
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
- Model calls: every purpose rebuilds its exact request (conversation calls
  from events matching the digests, standalone calls from their copy); summed usage matches
  provider usage; resume reload and request-time repair write no events.
- Gate: every tool call has exactly one decision; denied calls get an error
  result and no `tool.finish`; session grants are not restored.
- Versions: an unknown type or newer `v` blocks resume but still displays.
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
