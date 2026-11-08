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

Every event uses the table envelope below: identity/order, type/version/time,
optional correlation, cause, delivery and outcome columns, plus typed payload.
IDs are allocated before submit; seq is assigned at commit, never inferred from
timestamps. Payload does not duplicate envelope fields. A suffix `?` below means
optional; otherwise payload fields are required. Envelope requirements by type are
specified in the correlation contract. Native messages, tool definitions and todos
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
| `delegation.finish` | error?, reason? | Close the delegated attempt; link accepted result through its cause when present | End the child session or duplicate its result body |
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

### Correlation and provenance

Generic query/validation fields are columns, not buried in payload:

| Envelope fields | Meaning and required use |
| --- | --- |
| `correlation_id` | One operation identity within a session: required for inference/tool/delegation start/finish, permission request/decide, hook finish and compaction apply. Pairs share it; execution retries get new IDs, persistence retries keep theirs. Null on unrelated events. |
| `cause_session_id`, `cause_event_id` | One immediate causal event, both present or both absent. Generated messages/summaries reference their producing start; received messages reference source content, or delegation.start for delegated input. Follow links rather than copying a trace path. |
| `delivery_id` | Required on cross-session message acceptance; identifies one accepted batch and remains stable on redelivery. Null on other events. |
| `outcome` | Required only on inference/tool/delegation/hook finish: success/failure/cancelled. Permission decision stays in its typed payload. |

Correlation is not a global trace ID. It replaces payload attempt/request/invocation/
operation IDs for the event's own operation; native call_id and turn_id remain typed
payload fields. Compaction has its own correlation, distinct from the inference
identified by its cause. Causal columns replace producer_ref rather than mirror it.
An event may have no cause, for example direct user input; never invent one. Type,
local message role and the causal chain identify origin without a redundant source-kind
column. Event-specific input/content references remain typed payload: one immediate
cause is not a complete dependency graph.

### Validation and correlation

- Creation is the first event, at seq 1, and its identity must not exist;
  fork_source_id and fork_source_seq are either both absent or identify an existing
  nonempty source prefix. Initial defaults are typed configuration, not secrets.
  Fork constructs one child creation using the rules below, not a second creation.
- Native message IDs are nonempty and unique; same ID with different content is a
  conflict. Compaction validates its source and references as specified below.
- Generated inference/tool message batches and summaries must name their producing
  start through the causal columns. A batch has one cause; split mixed origins.
  Validate native tool-call IDs against that execution. Causes are immutable alongside
  content, never inferred from timestamps or adjacency. Legacy unknown provenance
  stays explicitly unknown through the compatibility mapping.
- Start/finish match correlation_id in the same session and execution family; tool
  call IDs also match. Admit one start and at most one terminal result per operation.
  Reusing a correlation for a different operation conflicts. Failure needs a bounded
  structured error; cancellation needs a reason. Unknown results have no fabricated
  finish. Repeating an identical result is idempotent; conflicting outcomes fail.
- Permission decisions match request correlation IDs; decision is allow/deny/cancelled.
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
one delegation correlation identifies one execution or continuation, not the child's
lifetime. The parent records only coordination facts and content actually accepted:

```text
Parent: tool.start → delegation.start
                         │ cause
                         ▼
Child:  session.create / accepted input → inference/tool/message events
                                                       │ result content
                                                       ▼
Parent: message.append ───────────────────────→ delegation.finish
        cause = child content                   cause = accepted result
        delivery_id = receipt                   correlation = delegation.start
```

Allocate the target child ID before committing delegation.start, then create/claim
the child before dispatch; a failed creation may leave the reserved ID without a
session. Validate current parent/task/child ownership before execution. A new child's
creation, or an existing child's accepted continuation input, causally references the
parent delegation start. Tool dispatch acknowledgement is not delegation completion.
Each continuation has a fresh delegation correlation. Known completion, failure or
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

The cause identifies the upstream content event, except delegated input references
delegation.start and resolves its immutable input_ref. One delivery is one same-origin
batch; multiple deliveries of one source event have distinct delivery IDs. The trusted
transport allocates collision-resistant delivery IDs, retains them across retries and
validates source/session ownership; message text cannot assert authority. Same
receiver/delivery plus identical content and provenance returns the existing acceptance;
a changed body or cause conflicts, even with a new event ID. Acknowledge only after
atomic acceptance. Acceptance does not imply model consumption; inference input IDs
determine what was used. Locally transformed content references its local producer,
whose input references lead to the accepted source; do not claim it is an exact copy.

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
| `CreateOptions` | Workspace ID, initial cwd, optional ID/parent and non-secret initial settings; paired `cause_session_id` / `cause_event_id` required for delegated creation, validated and persisted by Create |
| `Create` / `Open` | Return a writer-owned handle; Open claims before loading a consistent projection |
| `Get` / `List` | Read summaries without claiming a writer, including sessions owned by other processes |
| `SessionQuery` | Project/workspace/parent/group/sidechain/archive filters, bounded limit, opaque cursor |
| `Events` | Ordered raw events; cursor binds filters and fixed `ThroughSeq` so appends do not disturb pagination |
| `History` | Display projection of that fixed prefix, including pre-compaction history; attachments by reference |
| `Snapshot` | Immutable committed context, application state and recovery positions; no UI rows or live resources |
| `ForkOptions` | Optional destination workspace and source sequence; otherwise capture latest committed prefix |
| `CompactionChange` | `source_seq` (expected current prefix), stable `correlation_id` for the compaction operation, full native `summary` with stable ID/provider content, ordered `retained_message_ids`, causal identity for a generated summary |
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
| Operation/cause/delivery identities and terminal outcomes | First-class `session_events` columns; one authority, no payload mirrors |
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
| `id` | TEXT | Stable event identity |
| `type` | TEXT | Event discriminator |
| `schema_version` | INTEGER CHECK > 0 | Payload codec version |
| `created_at` | INTEGER | Event time, not ordering key |
| `correlation_id` | TEXT ? | Session-scoped operation identity shared by its lifecycle events |
| `cause_session_id` | TEXT ? | Session containing the immediate causal event; historical identity, not ownership |
| `cause_event_id` | TEXT ? | Event ID within cause_session_id; paired with that column |
| `delivery_id` | TEXT ? | Stable transport identity for one accepted cross-session message batch |
| `outcome` | TEXT ? CHECK success/failure/cancelled | Known execution terminal outcome; null for other types |
| `payload` | TEXT, validated JSON | Event-specific content only; no duplicate envelope fields |

PK `(session_id, seq)`; UNIQUE `(session_id, id)`, `(session_id, delivery_id)` and
`(session_id, type, correlation_id)` (null values do not conflict). The last constraint
prevents duplicate starts/terminals; application validation also forbids correlation
reuse across different operation families. Index `(session_id, correlation_id, seq)`
for operation lookup and `(cause_session_id, cause_event_id)` for reverse tracing.
Require both cause columns null or both nonnull. Validate required/forbidden columns
by type before commit. Foreign cause identities deliberately have no deletion FK:
never cascade, clear or block deletion through historical provenance. Local reference
integrity is enforced by append/import validation, not by fetching foreign data on replay.

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

## Compaction: first-phase session capability

Compaction changes active messages only; existing history, config, state and assets
remain intact. Model summaries may omit details: storage must be lossless, but
lossless summaries or model comprehension are not promised. `Session.Compact` uses
the shared append path; generate the summary outside any SQL transaction.

A successful compaction commits one `compaction.apply` event, not separate replacement
and completion events:

The envelope correlation identifies the compaction for idempotent retry; causal
columns identify its generating inference start. Neither is duplicated in payload.

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
Copy subsequent events in their original sequence positions and retain session-local
message/event/correlation/delivery IDs. Rebind a causal pair only when its session is
the fork source and its event belongs to the copied prefix, including references to
the replaced creation. Foreign provenance stays unchanged. Local event-sequence
anchors and historical compaction source_seq values keep their positions;
fork_source_seq denotes the source prefix. Copy assets and rewrite local content
references before publication. Copied delegation targets are historical identities:
do not reparent real children, inherit their execution ownership or redirect results.

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
   Report corrupt/unsupported sessions individually. Require verified lossless legacy
   mappings and old-projector parity before cutover; retain unmapped sources without
   claiming full compatibility. Never silently merge projects.
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
| Group switch/rejected reclaim; late background completion | No resurrected membership or misrouted callbacks |
| Delegation start, continuation, crash and fork | One correlation per execution; dispatch acknowledgement is not finish; unfinished attempts remain unknown; copied history grants no child ownership |
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
and replay costs before optimization. Documentation does not claim SQLite tests pass.

Before implementation, settle relocation/worktree-linking UX, final summary fields,
SDK transaction integration and migration bookkeeping. JSONL remains a valid
alternative if transaction/query benefits do not justify migration. An SQLite index
over files retains cross-store consistency work; per-project databases isolate
failures but complicate linking and global queries. Avoid speculative schemas.
