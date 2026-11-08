# PROP-0005: Session management and SQLite persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-07. The optional SQLite storage experiment and initial comparisons
are implemented; JSONL remains the default, with no automatic legacy
migration or default switch. The first stage uses existing scoped stores, the SDK's
native record codec and current transcript/truncate semantics. It does not implement
all 18 proposed event types or the logical Project/Workspace model below. The target
APIs and schema remain proposed; this changes persistence, not agent execution.

Runtime hooks and the inspector still require JSONL paths. Initial opt-in is through
a storage constructor/capability and tests, not an integrated TUI/ACP flag. Removing
those path assumptions is a separate integration gate.

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
                                            ├─ session.create
                                            ├─ message.append
                                            ├─ compaction.apply
                                            ├─ config.update
                                            ├─ state.update
                                            ├─ annotation.add
                                            ├─ inference.start
                                            ├─ inference.finish
                                            ├─ tool.start
                                            ├─ tool.finish
                                            ├─ delegation.start
                                            ├─ delegation.finish
                                            ├─ permission.request
                                            ├─ permission.decide
                                            ├─ hook.finish
                                            ├─ instruction.update
                                            ├─ toolset.update
                                            └─ skill.update

Events ── projector ──┬─ History: display history, including pre-compaction messages
                     ├─ Snapshot: active context + durable state for resume
                     └─ Summary: rebuildable session-list metadata
```

Each event row belongs to exactly one session. Its `type` selects the typed
`payload`; the categories above are not separate tables. Messages are event
content, not the entire event model. `message.append` owns native conversation appends;
`compaction.apply` owns its native summary. `tool.finish` adds execution/presentation
metadata, not a second copy of tool-result content. Parent/fork links relate
sessions; they do not share mutable event rows.
History and Snapshot are views of the same events, not independent saved copies.
Event means a durable persistence fact here, not every transient streaming/runtime
notification. `Event` is the proposed storage envelope; current code still uses
`transcript.Record` and requires an explicit adapter, not a blind type rename.
Types are flat `<object>.<action>` identifiers using base verbs. The event API,
commit boundary and replay contract make them recorded facts, not their grammatical
tense: compaction.apply never commands replay to generate a summary. Apply means
an accepted state transition; finish means a terminal outcome, not necessarily
success. Ownership is in `session_id`. These are target names, not the current
codec; import maps legacy types explicitly. Archive and current execution cwd
belong to session state, not home-workspace identity.

### Event application: boundaries and actions

The ownership path is projects → workspaces → sessions → session_events, through
project_id, workspace_id and session_id respectively. Each event belongs to one
session, not directly to a project. User/project configuration stays with its
existing owner; the session stream records only session overrides and execution
facts. An event without a defined payload, owner and consumer is not admitted.

```text
Live command → validate event + prepare deterministic projection
             → transaction: append event + update derived summaries
             → commit → publish projection → live coordinator reconciles if needed

Resume → read committed events → same projection rules → restore final state
       → resolve current configuration/trust before execution
```

“Trigger” below means the deterministic effect of applying a committed event.
It does not mean dispatching another command. Replay never calls a model, executes
a tool/Hook, prompts for permission or appends the same event again. Configuration
and cwd changes are reconciled by the live coordinator after commit; resume
reconciles final state once, not every intermediate historical transition.

### Event information contract

Every event uses the table envelope below: globally unique `id`, owning `session_id`,
commit-order `seq`, type/version/time, optional `source_event_id` and outcome, plus
typed JSON payload. IDs are allocated before submit; seq is assigned at commit,
never inferred from timestamps. Payload does not duplicate envelope fields. A suffix
`?` below means optional; otherwise payload fields are required. Envelope requirements
by type are specified in the identity contract. Native messages, tool definitions and todos
retain their SDK/product schemas rather than being reduced to text. Unknown optional
values are absent, not invented defaults. Exact Go types are an SDK-alignment gate.

| Event | Typed payload fields | Actions when applied | Does not do |
| --- | --- | --- | --- |
| `session.create` | workspace_id, initial_cwd, agent_id, sidechain; parent_session_id?, fork_source_id?, fork_source_seq?, initial_config? | Initialize identity and projection; attach fork provenance | Start an agent, create a project or repeat file copying |
| `message.append` | messages: nonempty ordered native-message list; source_message_ids? (aligned source IDs for received native messages) | Index messages; append history/active sequence; derive preview/counts | Invoke the model or execute embedded tool calls |
| `compaction.apply` | source_seq, summary: native message, retained_message_ids: ordered list | Index summary; switch active sequence to summary + retained messages | Delete history/assets or alter config/state |
| `config.update` | patch: ConfigPatch | Update session overrides; invalidate next effective-config resolution | Modify User/Project files, history or an in-flight request |
| `state.update` | patch: StatePatch | Update named state and summaries; mark environment stale on cwd change | Replace messages, change home workspace or grant authority |
| `inference.start` | turn_id, input_message_ids, request_config, instruction_digest, toolset_digest | Register unfinished attempt and actual non-secret request provenance | Make a model request |
| `inference.finish` | stop_reason?, usage?, duration_ms?, error?, reason? | Close attempt and update audit/usage; output stays in message events | Duplicate output or automatically retry |
| `tool.start` | call_id, input_ref | Register unfinished execution; input_ref identifies the durable invocation and arguments | Authorize or run the tool |
| `tool.finish` | call_id; duration_ms?, error?, reason?, details? | Close attempt and attach typed execution/display details; result body stays in messages/assets | Copy result bodies or repeat side effects |
| `delegation.start` | child_session_id, input_ref | Register one accepted child execution/continuation and its input | Launch a child during replay or copy its event stream |
| `delegation.finish` | result_event_id?, error?, reason? | Close the delegated attempt; link accepted result through result_event_id when present | End the child session or duplicate its result body |
| `permission.request` | operation_ref, scope | Add pending audit entry linked to the protected operation | Open a prompt during replay |
| `permission.decide` | decision, scope; reason? | Resolve the request's audit entry | Restore a historical grant as current authorization |
| `hook.finish` | hook_name, trigger; duration_ms?, reason?, effect_refs? | Record known Hook outcome and references to separately persisted effects | Run Hook or apply effects again |
| `instruction.update` | upsert: named instruction sections, remove: section names; source? | Update historical instruction catalogue used by request digests | Override current instruction files or inject messages |
| `toolset.update` | upsert: native tool definitions, remove: tool names; source? | Update historical tool catalogue used by request digests | Install tools or enable execution |
| `skill.update` | changes: named, typed skill activation states; source? | Update observed activation history for inspection | Load skills or edit preferences |
| `annotation.add` | text; anchor? (message ID or event sequence) | Add durable display-only commentary at the anchor, or session level if absent | Enter model context, change state or trigger execution |

Producers are explicit: Store.Create/Fork for identity; accepted-message adapters
for messages; Session.Compact for compaction; settings/session owners for patches;
model/tool execution adapters for attempt records; approval and Hook owners for
their audit; delegation coordinators for child execution facts; request preparation
for instruction/toolset changes; skill activation for observed skill changes;
application features for annotations. Renderers are consumers, never persistence sources.

ConfigPatch allows provider, model, generation_options, mode and autopilot_policy.
Generation options and autopilot policy use versioned, allowlisted product fields,
not arbitrary provider JSON. StatePatch allows title, archived_at, todos,
autopilot_progress, execution_cwd and group_hint. Nested values are typed; group_hint
is only group/member identity for reclaim, not roster or permission authority.
Patch fields distinguish absent (unchanged), set and clear; clear removes an override
or resets a field according to its typed contract. False/zero/empty are explicit
values, not an implicit clear. Reject unknown or cross-owner fields.
Unchanged patches and definition deltas are no-ops.

### Identity and provenance

| Envelope fields | Meaning and required use |
| --- | --- |
| `id`, `session_id`, `seq` | Globally unique event identity, owning session and session-local commit order. Persistence retries retain the event ID; new executions get new start event IDs. |
| `source_event_id` | Optional immediate source event, local or foreign. Generated messages/summaries name their producing start; received messages name source content, or delegation.start for delegated input. No source-session column is needed with global IDs. |
| `outcome` | Required only on inference/tool/delegation/hook finish: success/failure/cancelled. Permission decision stays in its typed payload. |

A start event's ID is the operation identity, not a separate generic correlation ID.
Finish and permission-decision events use `source_event_id` for their start/request.
When both operation and result relations are needed, delegation.finish uses
`source_event_id` for delegation.start and typed `result_event_id?` for the locally
accepted result. Permission.request retains typed `operation_ref` for its protected
operation. Other event-specific input/effect references stay in typed payload; one
source link is not a dependency graph. Compaction and Hook finish use their own
event IDs for retry identity. Direct user input may have no source; never invent one.
Native call_id and turn_id remain payload fields. Delivery deduplication uses the
stable acceptance event ID under the transport contract below, not another column.

### Validation and correlation

- Creation is the first event, at seq 1, and its identity must not exist;
  fork_source_id and fork_source_seq are either both absent or identify an existing
  nonempty source prefix. Initial defaults are typed configuration, not secrets.
  Fork constructs one child creation using the rules below, not a second creation.
- Native message IDs are nonempty and unique; same ID with different content is a
  conflict. Compaction validates its source and references as specified below.
- Generated inference/tool message batches and summaries must name their producing
  start through source_event_id. A batch has one source; split mixed origins.
  Validate native tool-call IDs against that execution. Sources are immutable alongside
  content, never inferred from timestamps or adjacency. Legacy unknown provenance
  stays explicitly unknown through the compatibility mapping.
- Finish source_event_id resolves to a start in the same session and execution family;
  tool call IDs also match. Admit at most one terminal result per start. Failure needs
  a bounded structured error; cancellation needs a reason. Unknown results have no
  fabricated finish. Repeating an identical result is idempotent; conflicting outcomes fail.
- Permission decisions name their request event ID through source_event_id;
  decision is allow/deny/cancelled.
  Scope cannot expand the request. A Hook dispatch acknowledgement is not a known
  finish. A Hook effect changes state only through its own event/owner, not replay.
- Local causes and required message/event/asset references resolve to the committed
  prefix or validated earlier entries in the same transaction. Foreign causal
  provenance and parent/fork identities are explicit cross-session exceptions.
  Trusted adapters validate foreign causes at acceptance; imported unavailable
  provenance requires an explicit compatibility mapping, not a fabricated reference.
  Replay resolves content locally and never requires a foreign cause to remain alive.
  input_ref identifies immutable invocation name/arguments, either a native call or
  persisted invocation asset. Missing required content fails; foreign-source deletion
  may limit tracing but never deletes locally accepted content.
- Instruction/toolset updates use unique names, disjoint upsert/remove sets and
  typed content; empty initial catalogues are known values, not missing history.
  Request digests must resolve to their recorded catalogue version. Skill changes
  have unique names and validated states. Do not copy unchanged definitions per call.
- Reject unknown type/version or invalid fields before commit. No-op commands emit
  no event. Legacy records lacking required fields need an explicit compatibility
  mapping preserving what was known; never fabricate execution facts to fit a schema.

### Delegation and cross-session input

A subagent owns its own session/event stream. Parent ancestry identifies the session;
one delegation.start event ID identifies one execution or continuation, not the child's
lifetime. The parent records only coordination facts and content actually accepted:

```text
Parent: tool.start → delegation.start
                         │ source_event_id
                         ▼
Child:  session.create / accepted input → inference/tool/message events
                                                       │ result content
                                                       ▼
Parent: message.append ───────────────────────→ delegation.finish
        source_event_id = child content         result_event_id = accepted result
        id = stable acceptance ID               source_event_id = delegation.start ID
```

Allocate the target child ID before committing delegation.start, then create/claim
the child before dispatch; a failed creation may leave the reserved ID without a
session. Validate current parent/task/child ownership before execution. A new child's
creation, or an existing child's accepted continuation input, causally references the
parent delegation start. Tool dispatch acknowledgement is not delegation completion.
Each continuation has a fresh delegation.start event ID. Known completion, failure or
cancellation closes that attempt; a crash leaves it unknown until reconciled, never
a reason for replay to launch work. Result messages and delegation.finish commit
together when known together; finish references the locally accepted result, not
another copy of its body. If there is no result body, finish needs no invented message.

Cross-session content uses message.append, not a second received-message type. The
receiver stores the accepted content and independently retained assets under new,
stable receiver-local message IDs. For native-message input, source_message_ids maps
each local message to its original ID within the causal source event; retain provider
fields and tool-call identities, not a second copy of the source body. Context order,
inference inputs and compaction use local IDs. Redelivery reuses the accepted IDs;
a genuinely new delivery gets new ones even for identical source content. This is
message acceptance, not a lossless import that preserves source-local message identity.

The source_event_id identifies upstream content, except delegated input references
delegation.start and resolves its immutable input_ref. One delivery is one same-origin
batch. The trusted transport durably maps (receiver, transport delivery key) to one
globally unique acceptance event ID before submission, retaining that mapping across
retries/restarts and ambiguous commits. Multiple deliveries of one source get distinct
acceptance IDs. Redelivery must not allocate a new event ID to bypass deduplication.
Validate source/session ownership; message text cannot assert authority. Same mapped
ID, content and provenance returns the existing acceptance; changed content, source
or receiver conflicts. Acknowledge only after atomic acceptance. The transport retains
its routing/retry guarantees; removing a delivery column does not remove this contract.
Acceptance does not imply model consumption; inference input IDs determine what was
used. Locally transformed content references its local producer, whose input references
lead to the accepted source; do not claim it is an exact copy.

Transport/group inbox owns routing, retry and delivery guarantees. Session history
records acceptance, not a parallel queue or every send/receive notification. No
cross-session transaction or exactly-once execution is implied. Late results are
routed to the original session's writer, never the selected UI or a fork. Source
identity survives deletion as provenance, but tracing may stop there. Heartbeats,
progress and forwarded child event streams are not persisted; replay never resends
messages, grants authority or takes ownership of a child.

### Consolidation rules

Creation and fork share one creation transition; fork provenance is optional data
on session.create, not another creation event. Successful, failed and cancelled
attempts share one finish transition with a required outcome; retries retain
separate attempt IDs and no conflicting terminal result may overwrite a known one.
Start means the attempt was admitted before dispatch, not proof of remote receipt.
An attempt with start but no finish remains unknown, never fabricated as
failure. This is a flat event catalogue, not a generic type/phase/action envelope.

Keep config intent separate from state facts; tool execution separate from toolset
definitions; instruction definitions separate from display annotations. Skill
history records observed activation for inspection, not another editable preference.
A compaction has just one apply event; no start/finish or replacement events are
introduced for it. Its summary-generation request may have inference audit records.

### Config versus state

These remain separate typed events: desired future behavior versus current session
facts. Session config overrides do not duplicate User/Project settings. An annotation
is neither: it is durable display commentary, such as a failed group-rejoin note.
It has no model role or state patch and follows the recording admission rule below.

| Owner | Allowed field families |
| --- | --- |
| `config.update` | Model/provider selection, generation options, operation mode, autopilot policy/limits |
| `state.update` | Title, archive marker, durable todos/progress, current execution cwd, group restoration hint |
| Other events/owners | Messages and compaction; actual per-request settings/usage; live processes, authorization and real group membership |

Config means intended future behavior; state means the session's current facts.
Each field has exactly one owner. Split autopilot policy from progress; never store
an opaque whole autopilot object in both. Patch validation follows the typed contract
above. Config/state replay restores intent/facts, not live permissions. Existing mixed StatePatch fields
must be mapped explicitly; last-prompt preview is derived from messages, not another
independent state setter. No catch-all context/settings event is introduced.

### Recording contract: complete facts, minimal duplication

Admit an event only if it adds a durable conversation fact, an accepted config/state
change, a necessary execution/authorization audit fact, or non-reconstructible
historical commentary. It needs a defined producer, consumer and replay meaning.
Being observable at runtime is not sufficient. Starts, failures, cancellations and
permission denials remain meaningful: they explain attempted work and recovery
uncertainty even without changing active context. This is not a general debug log.

Commit inference/tool start before dispatch. This boundary is separate from a
durable assistant tool call, which may exist before execution is authorized.
If required start persistence fails, do not dispatch. Adapters must establish
these target guarantees rather than assume current callbacks already provide them.
When completed output and its terminal outcome are known together, commit output
and finish in one Append batch. Earlier complete messages may already be committed;
their causal reference does not imply that the attempt finished. After a crash,
keep such output and leave the attempt unknown until reconciled; never dispatch it
on replay. Derive attempt outputs from immutable causal references, not a second
output list on finish events. Audited compaction uses the ordering specified below.

Do not store token deltas, spinner/progress ticks, picker navigation, repeated reads,
empty patches, unchanged snapshots or reconstructible UI text. Final native messages
remain complete. Preserve genuine repeated actions and identical user messages:
deduplicate retries by event/operation ID, never solely by text or timestamps.
Message batches deduplicate by native message identity; conflicting content for an
existing identity is invalid. Compaction references immutable retained messages,
not a duplicated active-context snapshot or a parallel messages table.
Secrets, credentials and unbounded diagnostic dumps are
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
| `CreateOptions` | Workspace ID, initial cwd, optional ID/parent and non-secret initial settings; `source_event_id` required for delegated creation, validated and persisted by Create |
| `Create` / `Open` | Return a writer-owned handle; Open claims before loading a consistent projection |
| `Get` / `List` | Read summaries without claiming a writer, including sessions owned by other processes |
| `SessionQuery` | Project/workspace/parent/group/sidechain/archive filters, bounded limit, opaque cursor |
| `Events` | Ordered raw events; cursor binds filters and fixed `ThroughSeq` so appends do not disturb pagination |
| `History` | Display projection of that fixed prefix, including pre-compaction history; attachments by reference |
| `Snapshot` | Immutable committed context, application state and recovery positions; no UI rows or live resources |
| `ForkOptions` | Optional destination workspace and source sequence; otherwise capture latest committed prefix |
| `CompactionChange` | `source_seq` (expected current prefix), stable event `id` for compaction retry, full native `summary` with stable ID/provider content, ordered `retained_message_ids`, optional `source_event_id` naming the generating inference start |
| `Append` / `Compact` | One shared atomic write implementation and compaction validation; Append also batches compaction with its inference finish, while Compact wraps a single transition |
| `Archive` / `Delete` | Archive appends state; hard deletion requires ownership and rejects an active writer |
| `Close` | Wait for in-flight writes and release ownership exactly once |

### Session lifetime versus execution lifetime

A Session is a resumable conversation, not one program invocation or agent run.
Its lifetime is therefore not a mandatory create/end pair.

| Operation | Durable effect | May resume? |
| --- | --- | --- |
| Create / Fork | Establish identity/provenance via session.create (fork includes source identity/sequence) | Yes |
| Open / Close | Acquire/release writer; Close retires only that handle, not the conversation | Yes |
| Finish / cancel execution | Record known inference/tool outcomes; messages remain | Yes |
| Crash | No reliable end event; incomplete attempts stay unknown until reconciled | Yes, with recovery checks |
| Archive / unarchive | State patch changes visibility, not writability or history | Yes |
| Delete | Explicitly remove the session/events and reclaim assets; not an end event | No |

Do not infer execution liveness from a last event. Runtime reports live execution;
the lease only determines ownership, including while idle.
No session.end event is defined initially. A future terminal event would require
an explicit rule forbidding further writes and defining reopening, not merely a
shutdown notification. Execution telemetry must not permanently end a session.

Already-open sessions archive through their own Append. `Archive(..., false)`
restores visibility. Project/workspace deletion is restricted while children exist;
archive does not delete history. Parent relationships must be acyclic.

Project history before paginating display entries: isolated raw event pages cannot
reconstruct compaction context transitions. Never resume from UI history. List is a live keyset
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
| Event identity, source and terminal outcome | `session_events` columns; operation/result-specific references stay in typed JSON payload |
| Native messages, compaction content, session overrides, todos, annotations and audit details | Typed `session_events.payload` |
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
| `id` | TEXT UNIQUE | Globally unique stable event identity |
| `type` | TEXT | Event discriminator |
| `schema_version` | INTEGER CHECK > 0 | Payload codec version |
| `created_at` | INTEGER | Event time, not ordering key |
| `source_event_id` | TEXT ? | Globally unique immediate source event ID; historical provenance, not ownership |
| `outcome` | TEXT ? CHECK success/failure/cancelled | Known execution terminal outcome; null for other types |
| `payload` | TEXT, validated JSON | Event-specific content only; no duplicate envelope fields |

PK `(session_id, seq)`; UNIQUE `(id)`. Index `(source_event_id)` for operation/source
lookup. Validate required/forbidden fields and at most one terminal per start/request
inside the append transaction; do not make all source links unique, since a start can
produce many messages. Source identities deliberately have no deletion FK: never
cascade, clear or block deletion through historical provenance. Append/import validates
local references; replay does not fetch foreign data.

Use one shared events table, not per-session tables or databases. Benchmark ordinary
rowid storage plus these indexes against `WITHOUT ROWID` with the composite primary
key; neither layout is assumed faster. Keep the five logical table definitions here,
including the optional checkpoint table, separate from the first-stage backend schema.

Promote shared identity, deduplication, causal lookup and terminal-status fields;
keep native content, scope, input references, patches and diagnostics typed in payload.
Do not add speculative columns for every optional field. Project/workspace identity
is obtained through the session join, not copied onto every event. Native message
IDs differ from event IDs; native role is not another envelope actor/source field.
The codec must preserve signatures, encrypted reasoning, image/content order,
tool IDs and supported provider fields. Current `Record.Message`/`Messages` use
`json:"-"`; directly marshaling Record would lose messages. Preserve current
legacy replacement semantics through a proven lossless import mapping, not a
replacement event in the target model. A message-parent tree is a separate decision.

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

## Compaction: target session capability

The backend experiment preserves current SDK compaction/truncate semantics; the
following `compaction.apply` contract is the target, not a first-stage codec change.

Compaction changes active messages only; existing history, config, state and assets
remain intact. Model summaries may omit details: storage must be lossless, but
lossless summaries or model comprehension are not promised. `Session.Compact` uses
the shared append path; generate the summary outside any SQL transaction.

A successful compaction commits one `compaction.apply` event, not separate replacement
and completion events:

The event id identifies compaction for idempotent retry; source_event_id identifies
its generating inference start when known. Neither is duplicated in payload.

| Payload field | Meaning |
| --- | --- |
| `source_seq` | Committed session version captured before summary generation, after its inference start if audited |
| `summary` | Complete native summary message, including stable ID, role, content and provider fields |
| `retained_message_ids` | IDs retained in original active-sequence order; no duplicated message bodies |

Project active context as summary followed by referenced messages. References must
resolve to immutable messages in the same session at the source version, including
valid earlier summaries. Index the new summary for History without another message
event. Preserve original text, tool results, images, signatures, content ordering
and IDs; do not leave orphaned tool results.

For audited generation, serialize input capture and inference.start persistence,
then take the resulting committed prefix as source_seq before dispatch. On accepted
success, one Append transaction validates that prefix and appends compaction.apply
followed by inference.finish. Compare against the tail before the batch, not its
final sequence. The audit start is already in the captured prefix; finish cannot
invalidate its own apply. Any intervening write makes the result stale under this
conservative rule. No separate context-version field is needed initially.

Before commit, validate unchanged source version, conflict-free summary identity,
existing unique references, order and native tool-call/result consistency.
Reject missing, cross-session or future references. Commit the event and derived
summaries/sequence in one transaction; publish the memory projection afterward.
Identical operation/content retries add no event; different content under the same
operation ID conflicts. Check prior commitment before rejecting stale source
versions. Reject results from retired session/runtime owners.

Failure, cancellation or no-op compaction commits no compaction event and leaves
context unchanged. Inference finish describes generation, not acceptance: a generated
summary rejected as stale may still have a successful inference outcome. Record a
known outcome without apply when needed; do not persist unused summary content just
for audit. If the apply/finish transaction fails, neither event is published; reconcile
ambiguous commitment before separately recording the known inference outcome.
After a committed crash, resume restores the result even if the UI never displayed it. Reconcile
ambiguous commit outcomes before retrying or accepting new writes. Auto, manual,
inactive-runtime and consecutive compactions share this rule. Fork follows the
construction rules below so retained IDs resolve locally and each session compacts
independently. Never delete old assets during compaction.
Checkpoints only accelerate replay and replace neither originals nor compaction
events. No dedicated compactions table is needed.

Explicit import mappings must preserve legacy summary-append/boundary/truncate
context, full history, native IDs and inspector provenance. Never relabel generic
noncompaction replacements as compaction. Fresh imports or child snapshots may use
message appends only after equivalence is demonstrated. Otherwise reject that
import, retain originals and block cutover until a lossless mapping is defined;
never silently discard records. Compare old/new projector context, history and
references, including repeated compaction, failure/retry, missing references,
resume and fork. These are acceptance contracts, not implementation guarantees
already established by this documentation.

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
- Publish assets atomically before references. Cleanup excludes in-progress
  operations and is retryable. Delete commits before asset reclamation.
- Back up a consistent SQLite snapshot and referenced assets together. Copying only
  the main database file during WAL writes is insufficient.

### Fork construction

Fork protects a fixed, nonempty committed source prefix against deletion until the
child is published. Replace the source creation at seq 1 with one child creation;
do not prepend it. Child identity/home-workspace options and fork provenance are
new; preserve the source initial configuration as the seed for inherited replay.
Copy subsequent events in their original sequence positions, preserving native message
IDs, but allocate fresh globally unique event IDs for every copied event and the child
creation. Build a complete old→new event-ID map, including the replaced creation.
Rewrite ALL source-local event references through it, in both envelope and typed
payload: source_event_id, operation_ref, result_event_id, input/effect references and
any other event-ID-bearing field. Start IDs identify operations, so copied operation
references must also change. Foreign source IDs remain unchanged; missing required
local mappings fail validation. Local event-sequence anchors and historical compaction
source_seq retain their positions; fork_source_seq denotes the source prefix. Forked
acceptances are historical copies, not new deliveries: never transfer the transport's
live receiver mapping. Copy assets and rewrite local content references before
publication. Copied delegation targets are historical identities: do not reparent real
children, inherit live execution/permission/group ownership or redirect results.

After projecting the inherited prefix, append child-local state patches for explicit
destination adjustments and, if set, clear group_hint. Do not filter historical
patches or rely on creation-time clearing. These patches must change a value; no
empty cleanup event is needed. Publish the creation, inherited events, final patches
and derived summaries atomically. Copying is validated import, not fresh command
dispatch: historical attempts remain history and unfinished ones are never restarted.
A fresh child replay must equal the Fork result, have no inherited group hint, and
resolve all content references without the source session, even after its deletion.

## Ownership boundary and rollout

sdk-go owns generic ordered storage, native codecs, ownership and transaction
primitives. San owns identity resolution, configuration/trust, application records
and projections. Define atomic SDK/San summary updates without SDK importing San
or UI/runtime issuing SQL. JSONL remains the default writable backend for existing
sessions; do not dual-write a session. Path-independent queries/export must eventually
replace `TranscriptPath` and file-existence assumptions before runtime integration.

1. **Backend experiment (approved):** offer an optional SQLite storage constructor/
   capability, using current scoped stores and SDK native record codecs, including
   existing replacement/truncate behavior. Test against current JSONL, not an imagined
   unoptimized baseline. Do not claim the target project catalog or event schema is built.
2. **Correctness and performance gate:** run the comparisons below, narrow lock scopes,
   update summaries incrementally and separate list/history/resume reads. Keep JSONL
   default; no automatic legacy import, default switch or TUI/ACP selection flag.
3. **Target model and integration (proposed):** settle identity, SDK transactions,
   summary columns and adapters for the 18-type schema; remove hook/inspector path
   dependencies and verify runtime consumers before exposing backend selection there.
4. **Migration/cutover (separate decision):** require explicit approval, exclusive
   source ownership, lossless mappings, preserved originals/assets and verified
   old-projector parity. Record progress/provenance, reject unsupported records and
   verify retries rather than overwrite. Never silently merge projects. Only after
   that decision may JSONL become interchange rather than the default writer.
5. **Measured optimization:** compare physical layouts/indexes; add checkpoints only
   if measured replay cost justifies them, not as a prerequisite for correctness.

Only one backend writes a migrated session. Before new SQLite writes, originals
permit rollback; afterward export newer history before rollback. Migration
bookkeeping schema follows the codec/legacy-ID audit. Classify old parent fields
from original records rather than guessing whether they mean ancestry or fork.

## Backend comparison and test acceptance

Use identical native-record fixtures, assets and durability guarantees. Report JSONL
flush/fsync policy, SQLite WAL/synchronous settings, driver/version, hardware, batch
sizes and cache conditions; never compare buffered acknowledgement to durable commit.
Measure current JSONL with its warm metadata/projection caches as well as cold opens,
and SQLite warm/cold paths. Report latency distributions, throughput, allocations,
database/WAL/index size and lock/busy time. The limited measurements below do not
replace this full acceptance scope.

- Cover many short sessions, long transcripts, large native payloads, single/batched
  appends, repeated compaction/truncate, fork, reopen/resume and concurrent sessions/
  processes. Compare rowid + indexes with `WITHOUT ROWID` under the same workload.
- Keep three read paths distinct: indexed summary Get/List (no transcript replay),
  bounded fixed-prefix History/Events browsing, and full active-context/state recovery
  for Open/Snapshot. History projection must still honor context transitions.
- Prepare encoding outside shared locks where safe; serialize per-session validation,
  commit and publication, keeping SQL write transactions short. Do not hold a global
  store lock over decoding, replay or unrelated reads. Update summaries from appended
  deltas, not a full-history rescan, with rebuild equivalence tests.
- Require JSONL/SQLite parity for native/provider fields, order, history, active context,
  state, assets, truncate and fork; test ownership conflicts, cancellation, crash/reopen,
  duplicate/conflicting writes, corruption and atomic record/summary publication.
  Assert no replay side effects. Capability gaps are explicit failures, not silent loss.
- Separate first-stage existing-record tests from future 18-type/global-ID mapping
  tests. Record reproducible commands and measured trade-offs before recommending a
  default change; checkpoints remain optional and require their own measured benefit.

### Initial measurements — 2026-11-07

Apple M5 / 32 GiB, darwin/arm64, Go 1.26.9, modernc.org/sqlite v1.44.3,
GOMAXPROCS=4. Values are medians of three benchmark-run averages, not P95/P99
or cold-disk latency. Fixture setup is excluded; OS caches are not flushed.
JSONL appends explicitly Sync; changed Save already syncs. SQLite uses WAL,
synchronous=FULL, fullfsync=ON and checkpoint_fullfsync=ON on every connection.
The latter settings match Go's macOS F_FULLFSYNC boundary; earlier measurements
without them are excluded rather than advertised as a write-speed improvement.

| Workload | JSONL ms/op | SQLite ms/op | Interpretation |
| --- | ---: | ---: | --- |
| San durable application append | 3.993 | 4.043 | Similar; more SQL allocations |
| Native append to 100-message context | 4.007 | 4.953 | About 24% slower |
| San replay, 1,000 messages | 2.947 | 3.534 | About 20% slower |
| List 100 sessions, warm summaries | 0.225 | 0.065 | About 3.5× faster |
| List after memory-cache loss, existing JSONL disk summaries | 1.490 | 37.900 | About 25× slower; SQLite lacks persisted product summaries |
| List with all summaries invalidated | 44.386 | 37.271 | About 16% faster |
| San Fork, 100 messages | 408.127 | 438.396 | About 7% slower; current per-record copy protocol |
| SDK independent durable writers, 4 workers | 3.110 | 5.465 | About 76% worse aggregate time/op; single DB writer contention |

Lists contain 100 messages per session. Replay allocates about 2.27 MB/op for
JSONL versus 3.80 MB/op for SQLite at 1,000 messages. Same-flags stripped San builds
are 82,744,498 versus 86,546,162 bytes: the pure-Go driver adds about 3.80 MB even
when JSONL is selected at runtime. This is an experimental cost, not zero-impact opt-in.

Layout comparison favors a conservative rowid table plus composite index initially:
WITHOUT ROWID replay was about 5–8% faster in the 1,000-message 256-byte/8-KiB
fixtures, but the 100 × 1-KiB append fixture occupied 487,424 versus 159,744 database
bytes after checkpoint. Live DB/WAL/SHM were 2,195,776 versus 1,577,776 bytes.
Neither layout wins universally; keep the comparison benchmark rather than adding
per-session tables or a public layout switch.

Reproduce in San and sdk-go respectively, without simultaneous benchmark runs:

```bash
go test ./internal/transcript -run '^$' -bench '^BenchmarkBackend' -benchmem -benchtime=30x -count=3 -cpu=4 -timeout=10m
go test ./pkg/session -run '^$' -bench '^Benchmark(SessionBackends|SQLiteLayouts)$' -benchmem -benchtime=100x -count=3 -cpu=4 -timeout=10m
```

Decision: keep JSONL default. Before runtime rollout, persist/rebuild product summaries
without full cold-list replay, reduce per-record Fork commits and replay allocations,
and validate writer contention. Listing now has a separate summary lock and read-only
history avoids the writer-cache lock; other writer/projection operations remain globally
serialized. SDK Save/Fork are atomic, but San compound compaction/Fork still use their
existing multi-call protocols. No automatic import, target-ID conversion, checkpoint,
full runtime integration, power-loss certification or tail-latency claim is included.

## Acceptance and remaining decisions

The following matrix describes target-model acceptance, not features already proven
by the first-stage backend experiment.

| Scenario | Required result |
| --- | --- |
| Roots, nested cwd, clones/worktrees and legacy partitions | Predictable identity; no silent merging or trust inheritance |
| Move, missing directory, path reuse, cross-project `cd` | Inspectable history; explicit reconciliation; execution policy refreshed |
| TUI/headless/ACP and competing processes | Validated cwd and common single-writer ownership |
| Group switch/rejected reclaim; late background completion | No resurrected membership or misrouted callbacks |
| Delegation start, continuation, crash and fork | One start-event identity per execution; dispatch acknowledgement is not finish; unfinished attempts remain unknown; copied history grants no child ownership |
| Cross-session delivery, redelivery and source deletion | Atomic local acceptance before acknowledgement; same delivery/content is idempotent, changed content/cause conflicts; accepted content survives source deletion; replay never resends |
| Child history and fork; parent/source deletion | Independent identity/assets; fresh replay equals Fork result, preserves local sequence references and clears inherited group hint; no implied live task |
| Repeated/failed/audited compaction, retry and invalid references | One atomic transition with one apply and its pending inference finish; own audit does not cause staleness; intervening writes reject apply; context/history parity and unchanged config/state/assets |
| Configuration changes and revoked permissions | Correct scoped writes and current security policy on resume |
| Config/state patches and mixed legacy fields | One typed owner per field; reject unknown keys; derive preview rather than duplicate it |
| Replaying every event type | Same projected state without model/tool/Hook calls, permission prompts or new events |
| Close/reopen, finish/cancel, archive and crash | Preserve resumable identity; no fabricated terminal event; unknown attempts remain unknown |
| Durable annotations versus derived UI text | Preserve meaningful anchored commentary without model injection or duplicate rendering events |
| Display history, images and parallel tools | Complete browsing without expanding resumed model context |
| Crash, duplicate writes, cleanup and bad checkpoints | Consistent records/summaries; recoverable caches; explicit failures |
| Interrupted calls, retry and repeated identical input | Output identifies its producer; committed output without finish remains recoverable but outcome unknown; known outcomes close the matching attempt; distinct actions survive deduplication |
| Event admission, streaming/progress and unchanged state | Preserve new durable facts and necessary failure/denial audit; no per-token events, empty patches, repeated snapshots or derivable annotations |
| Import, repeated import, export and rollback | Preserved IDs/content and one writable authority |

Use existing session/transcript/app tests as the baseline. Check picker responsiveness
and replay costs before optimization. Passing backend tests does not establish the
remaining target-model and migration guarantees.

Before implementation, settle relocation/worktree-linking UX, final summary fields,
SDK transaction integration and migration bookkeeping. JSONL remains a valid
alternative if transaction/query benefits do not justify migration. An SQLite index
over files retains cross-store consistency work; per-project databases isolate
failures but complicate linking and global queries. Avoid speculative schemas.
