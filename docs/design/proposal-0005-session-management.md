# PROP-0005: Session management and file-backed persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-07. This PR reviews design only: a storage-neutral session/event API
backed by files/JSONL. SQLite is not selected; historical measurements remain as the
rationale, without database migration or a backend-selection UI.

The companion [implementation PR #737](https://github.com/genai-io/san/pull/737) is
not yet merged. Implementation status below refers to that branch, not capabilities
added by this design PR or already on main. Its first stage implements `OpenStore`
project registration, shared Git worktree session ownership, current-checkout project
settings, exact non-Git directory identity, and optional project/workspace
fields in schema 4 with upgraded summary caches. That branch is being adjusted to
this configuration decision; it is not verification of the final behavior. Legacy cwd partitions are not migrated.
The SDK native record and transcript/truncate semantics remain: the 18-event format,
full target API, atomic batches, trash and automatic retention are not implemented.
Hooks and the inspector retain their JSONL integration. New-format cutover first needs
SDK atomic batches and error-returning durable admission before execution, not event renaming.

The design keeps one authority per fact. Persist durable conversation changes,
necessary execution/permission audit and non-reconstructible commentary, not a
general runtime log. Summaries and indexes must be rebuildable.

## Identity, directories and configuration

```text
checkout A --+
             +-- Git common directory --> one Project --> shared sessions
checkout B --+                              (workspace = A or B)

checkout A --> A's project config / tracked files (branch A)
checkout B --> B's project config / tracked files (branch B)
user scope --> naturally shared user-global config
```

Workspace is not an entity, service or physical directory layer. A Session is a
resumable conversation, not one process or agent run; Close releases a handle and
does not end it. There is no initial `session.end` event. Runtime owns execution
and cancellation; stored todos, permission decisions and group hints are not live resources.

Project IDs are stable opaque IDs assigned at registration, separate from lookup paths:

| Startup location | Internal registration rule |
| --- | --- |
| Git | Canonical absolute Git common directory; resolve Git's relative result against its documented base first. Main checkout, linked worktrees and subdirectories share one Project. Independent clones and submodules remain distinct. |
| Non-Git | Exact normalized startup cwd is the key, project root and workspace. The same path reuses registration; startup in a child directory is a distinct Project. No nearest-ancestor lookup. |
| Both | Normalize absolute paths and symlinks consistently with trust checks; do not lowercase. A runtime cwd change never reassigns ownership. |

`project_id` and `workspace` are fixed creation attributes. Git workspace is the
checkout root; `initial_cwd` may be below it. Mutable `execution_cwd` can cross
projects without moving the session. Restore it from state, falling back to initial
cwd for old history; resolve tools, settings, hooks and trust for the execution location.

Moves, reused paths and missing/changed Git metadata require explicit validation
and registration reconciliation/selection. Never silently transfer identity,
merge old exact-cwd partitions or rewrite historical paths. Association does not
confer trust. Missing checkouts do not prevent history inspection.

Project identity/session sharing does **not** centralize configuration:

- Git project settings come from the **current checkout root**, not the original main
  checkout. Non-Git project settings use the exact startup cwd. Git-tracked settings,
  instructions and other files follow normal checkout behavior; branches may intentionally differ.
- No compulsory shared configuration root, automatic copying/synchronization or fallback
  to another checkout. History/session management does not require the main checkout to exist.
  User-global configuration is naturally shared; explicit links are opt-in.
- Existing configuration, MCP, plugin, skill and instruction loaders need no redesign
  merely to share Project identity and sessions; their existing path rules remain,
  not a claim that every loader normalizes subdirectories to checkout root. Preserve applicable legacy local-source
  and trust compatibility; this decision does not abolish all existing fallback behavior.

Preserve user -> project -> local resolution, applicable compatibility sources, persona
and field-specific merges; see [configuration](../reference/configuration.md). Writes
select one scope, update only its fields, coordinate across processes and reject read-only
sources; never write the merged result. Resolution retains per-field provenance.
Registration lock waits cannot yet be cancelled mid-wait; atomic publication does not
promise power-loss durability, and identical-path repository replacement is not automatically detected.

Session overrides affect subsequent resolution, not in-flight requests. Resume
uses current policy and credentials, never historical grants. Missing providers/models
need an explicit error or replacement. No transaction spans configuration files and
events; do not copy secrets, security authority or the full settings tree into history.

Legacy storage uses exact-cwd hash partitions; new registrations use stable project IDs.
Under `~/.san/projects/<project-id>/`, `project.json` holds identity/lookup metadata,
not configuration. `transcripts/<sid>.jsonl` holds the log, `.jsonl.meta` its rebuildable
summary, and `blobs/{image,tool-result}/<sid>/` session-owned assets. Proposed
`trash/<sid>/` retains deletion generations, manifests, logs and assets.

`project.json` is implemented for new registrations; trash remains proposed. Scanning
registrations rebuilds lookup indexes, not another identity authority. Old `<cwd-hash>`
partitions are not automatically renamed/moved. Worktrees share this partition and
Session.workspace distinguishes ownership paths; parent/child logs are peers linked
by parent_session_id. Configuration stays with existing loaders in the current checkout
or user scope, never copied into the session partition. Trash transitions must recover
without assuming atomic multi-file moves.

The internal catalog owns project name/description, registration and partition
locations, not configuration or current cwd. Existing partitions remain distinct
and readable until approved reconciliation; no automatic rename/import is implied.
Project removal is blocked while sessions, trash or unfinished transitions remain.
Deleting a session never deletes its checkout; ancestry never implies cascade deletion.

## Event contract

An Event is an immutable durable fact, not a transient notification or a renamed
`transcript.Record`. Names use `<object>.<action>`: apply records an accepted
transition; finish records a terminal outcome, not necessarily success.

| Envelope field | Type and meaning |
| --- | --- |
| `id` | Globally unique string, allocated before submission; stable across persistence retries |
| `session_id` | Owning session ID; project/workspace are obtained through that session |
| `seq` | Contiguous positive session-local commit order, assigned at commit |
| `type`, `schema_version` | Event discriminator and positive payload codec version |
| `created_at` | UTC Unix milliseconds; not an ordering key |
| `source_id?` | Immediate source **event ID**, local or foreign; not a session, request, native message ID or legacy sequence |
| `outcome?` | Required only on inference/tool/delegation/hook finish: success/failure/cancelled |
| `payload` | Validated typed JSON, without duplicate envelope fields |

A start event's ID is the operation identity. Finish names its start through
`source_id`; permission.decide names its request. Generated messages/summaries name
the producing start. Received messages name source content, or delegation.start
for delegated input. Direct user input may have no source. Native call_id/turn_id
remain payload fields. A source link is provenance, not ownership or a dependency graph.

In the single type table below, `?` means optional; other payload fields are required.
Native messages, tool definitions and nested state keep their SDK/product types,
not generic strings. Exact Go payload types remain an SDK-alignment gate.

| Event | Typed payload fields | Effect when applied / boundary |
| --- | --- | --- |
| `session.create` | project_id, workspace, initial_cwd, agent_id, sidechain; parent_session_id?, fork_source_id?, fork_source_seq?, initial_config? | Initialize identity/projection and provenance; do not launch an agent or repeat copying. |
| `message.append` | messages: nonempty ordered native-message list; source_message_ids?: aligned source native IDs | Index content, append history/active sequence, derive previews; do not execute embedded tool calls. |
| `compaction.apply` | source_seq, summary: native message, retained_message_ids: ordered list | Index summary; active sequence becomes summary + retained messages; preserve history/config/state/assets. |
| `config.update` | patch: ConfigPatch | Update session overrides for next resolution, not User/Project files or in-flight requests. |
| `state.update` | patch: StatePatch | Update named state/summaries; cwd changes require environment refresh, not ownership or authority changes. |
| `inference.start` | turn_id, input_message_ids, request_config, instruction_digest, toolset_digest | Register unfinished attempt and actual non-secret request provenance; no model call. |
| `inference.finish` | stop_reason?, usage?, duration_ms?, error?, reason? | Close attempt and update audit/usage; output remains in message events; no automatic retry. |
| `tool.start` | call_id, input_ref | Register execution; input_ref identifies durable invocation/arguments; no authorization or execution. |
| `tool.finish` | call_id; duration_ms?, error?, reason?, details? | Close attempt with typed execution/display details; result bodies remain in messages/assets. |
| `delegation.start` | child_session_id, input_ref | Register one accepted child execution/continuation; no child launch or stream copying. |
| `delegation.finish` | result_event_id?, error?, reason? | Close attempt and link locally accepted result, not end the child or duplicate its result. |
| `permission.request` | operation_ref, scope | Record pending audit linked to protected operation; no replay prompt. |
| `permission.decide` | decision, scope; reason? | Resolve audit with allow/deny/cancelled; never restore historical authorization. |
| `hook.finish` | hook_name, trigger; duration_ms?, reason?, effect_refs? | Record known outcome and separately owned effect references; never rerun Hook/effects. |
| `instruction.update` | upsert: named instruction sections, remove: section names; source? | Update historical catalogue for request digests, not current files or messages. |
| `toolset.update` | upsert: native tool definitions, remove: tool names; source? | Update historical tool catalogue; do not install or enable tools. |
| `skill.update` | changes: named typed skill activation states; source? | Record observed activation for inspection, not load skills or edit preferences. |
| `annotation.add` | text; anchor?: message ID or event sequence | Durable display-only commentary at anchor or session level; never model context/state/execution. |

ConfigPatch permits provider, model, generation_options, mode and autopilot_policy.
Generation options/policy use versioned allowlisted product fields, not arbitrary
provider JSON. StatePatch permits title, archived_at, pinned, todos,
autopilot_progress, execution_cwd and group_hint. The hint contains group/member
identity for reclaim, not roster or permission authority. Last-prompt preview is derived.

Patches distinguish absent, set and clear; false/zero/empty are explicit values.
Reject unknown/cross-owner fields. Named catalogue changes require unique names,
disjoint upsert/remove sets and resolvable request digests; empty catalogues are known
values. Unchanged patches/definitions emit nothing.

Validation and recording rules:

- Creation is seq 1 and unique; parent links are acyclic. Fork source ID/sequence
  are both absent or identify a nonempty source prefix. Initial config contains no secrets.
- Native message IDs are nonempty and unique locally; conflicting content fails.
  Preserve signatures, encrypted reasoning, images/content order, tool IDs and
  supported provider fields. Current Record.Message/Messages use `json:"-"`;
  directly marshaling Record would lose content.
- Each message batch has one source; split mixed origins. Sources are immutable,
  never inferred from adjacency or timestamps. Local references resolve in the
  committed prefix or earlier validated batch entries. input_ref identifies immutable
  native-call or persisted invocation content. Required missing content fails.
- Finish must match a same-session start/family and tool call ID where applicable;
  at most one terminal result per start/request. Failure needs bounded structured
  error; cancellation needs a reason. Permission scope cannot expand its request.
  Unknown outcomes stay unknown; dispatch acknowledgements are not finishes.
- Trusted adapters validate foreign provenance at acceptance; replay reads local
  content without requiring the foreign source to survive. Imports with unavailable
  provenance need explicit compatibility handling, not fabricated references.
- Commit starts before dispatch; persistence failure prevents execution. When output
  and finish are known together, commit them in one batch. Earlier complete output
  may survive a crash without a known finish. No replay retries or side effects.
- Same event ID/content is idempotent; conflicting reuse fails. New executions and
  intentional repeat inputs get new IDs, even with identical text. Do not store token
  deltas, heartbeats, progress ticks, empty patches, repeated snapshots or derivable UI text.
  Exclude secrets and unbounded diagnostics. Renderers consume facts, never produce them.

```text
runtime adapters --> validate + commit batch --> committed Events (seq)
                         |                          |
                    failure: no dispatch            +--> pure projector
                    start committed: dispatch              |-- History (full)
                                                          |-- Snapshot (active)
                                                          `-- Summary (list)
replay --------------------------------------------> projector only; no effects
```

Replay only projects committed facts. It never calls models/tools/Hooks, prompts,
resends deliveries or appends events. After commit the live coordinator reconciles
runtime changes; resume resolves final configuration/trust once.

## Current records → target events

Current storage combines SDK message/truncate/application records with San schema
`"4"` and **19 type constants**, not these 18 events. See
[records.go](../../internal/transcript/records.go) and
[read.go](../../internal/transcript/read.go). Current read IDs are log positions.
The following is a semantic adapter proposal, not a rename migration.

| Current record(s) | Target / compatibility requirement |
| --- | --- |
| `session.started`, `session.forked` | One `session.create` with verified parent/fork provenance |
| `message.appended` (SDK message) | `message.append`, preserving native fields/IDs and known provenance |
| `session.compacted`, `conversation.replaced` (SDK truncate) | Jointly verify summary/boundary/truncate for `compaction.apply`; generic replacement is not necessarily compaction |
| `session.state.patched` | Split desired behavior into config.update and facts into state.update; derive preview |
| `inference.requested` | inference.start with validated request correlation/provenance |
| `inference.responded`, `inference.failed` | inference.finish with known outcome and recorded attempt relationships; do not infer missing starts |
| `system.section.added`, `system.section.removed` | instruction.update upsert/remove |
| `tool.added`, `tool.removed` | toolset.update, not execution |
| `hook.fired` | hook.finish only for known terminal outcome; async dispatch is not completion |
| `permission.required`, `permission.decided` | permission.request/decide; translate permit/reject, validate links; immediate decisions may lack requests |
| `skill.state.changed`, `notice.added` | skill.update / annotation.add with observed state and meaningful anchor semantics |
| `tool.completed` | tool.finish details only with known outcome/start correlation; current payload alone is insufficient |
| No dedicated schema-4 constant | tool.start and delegation.start/finish require new producers |

Missing starts, global IDs, links or outcomes require explicit compatibility mapping.
Do not discard unmatched records or fabricate facts. Unsupported lossless mappings
block import and cutover while originals remain intact. Prove old/new active-context,
full-history, native-ID, asset and inspector-provenance parity, including legacy replacements.

## API

The public model is a concrete project-scoped `*Store` and owned `*Session`, not
public Projects, Settings, Workspaces, SessionStore, EventReader or History services.
Consumers may define narrow interfaces; final primitives/types must align with sdk-go.

Current `internal/session.Store` has Create, Open, List/ListContext, Fork, Delete and
Close; Open/Fork also return History. Session.Append accepts one transcript.Record;
Session.Close exists, and transcript.Store.Records exposes raw records. Current Delete
immediately removes history. The following signatures and recoverable Delete are proposed.

```go
func OpenStore(ctx context.Context, dir string) (*Store, error)

func (s *Store) Create(ctx context.Context, opts CreateOptions) (*Session, error)
func (s *Store) Open(ctx context.Context, id string) (*Session, error)
func (s *Store) Get(ctx context.Context, id string) (Summary, error)
func (s *Store) List(ctx context.Context, query SessionQuery) (SessionPage, error)
func (s *Store) Events(ctx context.Context, id string, query EventQuery) (EventPage, error)
func (s *Store) Fork(ctx context.Context, id string, opts ForkOptions) (*Session, error)
func (s *Store) Archive(ctx context.Context, id string, archived bool) error
func (s *Store) Delete(ctx context.Context, id string) error
func (s *Store) Restore(ctx context.Context, id string) error
func (s *Store) Clean(ctx context.Context, opts CleanOptions) (CleanResult, error)
func (s *Store) Close() error

type CleanOptions struct {
    DryRun bool
    IDs    []string
    Limit  int
}

func (s *Session) ID() string
func (s *Session) Append(ctx context.Context, events ...Event) error
func (s *Session) Compact(ctx context.Context, change CompactionChange) error
func (s *Session) Snapshot() (Projection, error)
func (s *Session) Close() error
```

OpenStore normalizes startup dir, resolves/registers the project internally and fixes
its scope. All IDs must belong there except an explicitly selected Fork source.
I/O honors context cancellation; Snapshot reads an immutable in-memory committed projection.

| Type / operation | Options and behavior |
| --- | --- |
| CreateOptions | Optional ID/parent, non-secret initial settings, workspace/initial cwd. Defaults are captured checkout/root and startup dir; alternate workspace must resolve to the same project, with cwd defaulting there. Store supplies project_id; validate trust and delegated source_id. |
| Create / Open | Claim writer ownership before loading consistent state. Open preserves existing workspace/cwd, never constructor defaults. |
| Get / List / SessionQuery | Read summaries without claiming writers, across this project's workspaces. Filter workspace/parent/group/sidechain/archive, activity (`recent` or `all`), visibility (`live`, `trash`, `all`), bounded limit and opaque cursor. |
| Events / EventQuery / EventPage | Bounded seq-ordered raw events; cursor binds project/session, filters, lifecycle generation and validated fixed ThroughSeq/prefix identity. Later appends do not shift it; changed/unavailable content fails explicitly. |
| Snapshot | Active context, application state and recovery positions, not display rows or live resources. |
| ForkOptions | Source seq defaults to latest committed prefix; same-project workspace defaults to source. Receiver is destination; cross-project fork requires explicit separately opened source *Store, with destination defaults from receiver. Validate both associations/trust. |
| CompactionChange | Expected source_seq, stable event id, full native summary with stable ID, ordered retained_message_ids, optional source_id for generating inference start. |
| Append / Compact | Shared validated atomic batch path; Append supports apply + inference.finish, Compact wraps one transition. Creation is reserved for Create/Fork. |
| Delete / Restore / Clean | Recoverable deletion / validated restoration of original ID / bounded policy cleanup. Clean IDs only narrow scope; Limit has finite default and maximum; DryRun is read-only. |
| Session.Close / Store.Close | Drain writes and release Session ownership once. Close sessions first; Store drains operations and rejects closure while writer handles remain. |

List is a live keyset listing ordered by `(updated_at, id)`, not a cross-request
snapshot; unknown activity has a stable ID-ordered bucket. History is a product
projection of a complete fixed prefix, then display pagination—not an isolated raw
page or filtered stream. Never resume from UI history. Optional history-capable
checkpoints could accelerate this later; active-context-only caches cannot replace History.

| API / producer | Durable mapping |
| --- | --- |
| Create / Fork | One session.create; Fork imports/remaps inherited facts |
| Compact / audited Append | compaction.apply; accepted audited result batches apply + inference.finish |
| Archive | state.update.archived_at under exclusive ownership; already-open sessions use their own Append |
| Title/pin/todos/cwd/config owners | Typed state.update/config.update through Append; file settings stay with existing owner |
| Model/tool/delegation adapters | Starts, accepted messages and known finishes |
| Approval/Hook/request preparation/skill/application | Their typed audit/catalogue/annotation events, not separate public Store verbs |
| Get/List/Events/Snapshot, Open/Close | No conversation event; successful explicit resume activity is separate control metadata |
| Delete/Restore/Clean | External lifecycle metadata, never fake conversation events |

Resume is Open → projection → current configuration/trust → runtime. TUI/headless/ACP
share ownership rules; ACP checks requested cwd against restored execution location
before constructing services. Mismatch requires an explicit permitted change. Failed
opens release ownership; failed switches retain selection. Distinguish missing, busy,
closed, conflict, invalid, unsupported schema, corruption, cancellation and storage errors.

## Subagents, fork and compaction

```text
parent: delegation.start D -- dispatch --> child: independent event stream
        (reserve child ID)                           |
        [message.append R + delegation.finish] <----+ accepted result
         R.source_id = child content event           (copy content/assets)
         finish.source_id = D; result_event_id = R
```

**Subagents.** Before dispatch create/claim and validate parent/task/child ownership;
each continuation has a new start ID. Parent stores coordination and accepted content,
not forwarded child streams. Commit known result + finish together; never invent empty output.
Acceptance copies content/assets independently, using fresh receiver-local native IDs
and aligned source_message_ids, preserving provider fields/tool identities. Trusted delivery
keeps stable acceptance event/message IDs across retries/restarts; changed content/source/
receiver conflicts, intentional new deliveries get new IDs. Acknowledge only after acceptance;
inference input IDs—not receipt—prove consumption. Transport/group inbox owns routing/retry:
no parallel session queue, cross-session transaction or exactly-once execution guarantee.
Late results belong to the original writer, not selected UI/fork. Source deletion may end
tracing, never local retention. External group roster/inbox ownership remains unchanged:
resume reconciles/reclaims hints, switch releases membership, fork clears hints. Replay
cannot recreate processes/callbacks/child ownership; live continuation needs fresh validation.

**Fork.** Protect a fixed nonempty source prefix through publication. Replace its seq-1
creation with one child creation; retain source initial configuration for inherited replay.
Copy later events at original sequence positions with fresh global event IDs, preserving
native message IDs. Remap **all** source-local event references in envelope and payload,
including source_id, operation_ref, result_event_id and input/effect references; missing
required mappings fail. Foreign causes and local sequence anchors remain unchanged.

Copy assets independently and rewrite content references before publication. Historical
delegations do not reparent children, transfer delivery mappings or grant execution,
permission/group authority. After inherited replay, append necessary child-local destination
adjustments and clear group_hint if set; do not filter historical patches or emit empty ones.
Publish the staged transcript as a whole. Fresh replay must equal Fork's result and resolve
all content after source deletion; historical unfinished attempts are not restarted.

**Compaction.** Only active context changes to native summary + ordered retained messages.
Index summary for History without another message event; preserve history/config/state/assets.
Storage is lossless, model summaries need not be. Use the shared Append/Compact path:
serialize input capture and inference.start before generation, outside the commit lock.
Validate against the **pre-batch tail**: its own finish cannot invalidate apply, but any
intervening write does. Summary identity and same-session existing unique references must
preserve active order/tool-call-result consistency; earlier active summaries may be retained.

```text
commit inference.start --> capture source_seq S --> generate outside commit lock
                                                        |
                    retry: check prior commitment <-----+
                    owner valid + pre-batch tail == S?
                      yes --> [compaction.apply + inference.finish] atomic batch
                      no  --> no apply; context unchanged (finish may still be known)
```

Check prior commitment before rejecting stale retries; same ID/content adds nothing,
conflicts fail. Reject retired-owner results. Failure/cancellation/no-op leaves context
unchanged. A stale rejected summary may still have successful inference.finish without
apply or unused summary content. Resolve ambiguous batch commitment before recording
another outcome or retrying. All compaction modes and consecutive compactions use this rule.

## JSONL consistency and caches

These are **proposed requirements**, not guarantees supplied by ordinary JSONL append.
Initial guarantees cover local files, not network filesystems. sdk-go should supply
generic native codecs, ownership and commit primitives; San supplies application policy.

- Multiple lines are not atomic, even in one write. Append/Compact need a versioned,
  verifiable committed-batch boundary that binds identity, expected previous tail,
  sequence/count and integrity. Exact framing is an SDK integration decision, not
  another event type. Validate/prepare projection first; acknowledge and publish only
  after the complete batch reaches the specified flush/fsync boundary. Readers see no partial batch.
- Recovery exposes only validated committed prefixes. Ignore incomplete tail batches;
  repair under exclusive ownership before more writes. Corruption inside committed
  history fails, never skips. Stable IDs/content reconcile ambiguous commits; cancellation
  does not prove rollback. Unknown state-affecting versions block resume.
- Enforce fenced per-session cross-process writer ownership, including idle handles.
  Do not rely on timestamp-only leases or unlink a live lock to acquire it. Keep commit
  critical sections out of inference, tools and approval. Process death releases ownership,
  not proof that an external operation completed; external execution is not exactly-once.
- Publish/sync assets before references. Create/Fork stage validated content and expose
  only complete publication; discovery ignores staging. Backups protect committed prefixes,
  registrations, authoritative control/lifecycle metadata, trash and referenced assets
  together. Independently copying changing logs/blobs is not a consistent backup.

| Data | Authority / cache rule |
| --- | --- |
| Project identity/locations | Coordinated versioned internal catalog; atomic publication |
| Session identity and conversation | Creation and ordered typed events; no parallel messages/state/settings store |
| Resume activity and trash lifecycle | Authoritative versioned control sidecar / lifecycle metadata, included in backups |
| `.meta`, locators and indexes | Rebuildable; validate format/projector, committed prefix and relevant control/lifecycle revisions |
| Optional checkpoints | Deferred until measured benefit; no secrets/live resources; invalid caches fall back to events |

Caches may lag after crashes but cannot lead committed history, hide committed sessions
or resurrect deleted ones. Validate/rebuild before coherent reads; coordinate cross-process
invalidation. Publish caches atomically and memory only after event commitment; no physical
multi-file atomicity is claimed. Global event-ID collision/retry checks must work across
partitions, including imports; random allocation alone is not validation.
Keep file-format, codec and projector versions separate. Do not prescribe speculative cache schemas.

## Retention and safe cleanup

Default: **no automatic deletion of live history**. Recent filtering, archive, pin,
recoverable Delete and permanent Clean are distinct; compaction is none of them.

| Policy | Default / effect |
| --- | --- |
| Recent list | 30 days, display filter only. Pinned bypass age, not explicit archive filters; unknown activity remains visible. |
| Pin | false; when set blocks manual/bulk Delete and automatic retention. Explicitly unpin; no hidden force option. |
| Trash | 7 days after soft deletion. Show deadline before confirmation; Delete authorizes later expiry cleanup, never early purge. |
| Automatic retention | Disabled. Explicitly enabled inactivity age (e.g. 90 days) moves eligible live/archived sessions to trash and authorizes subsequent expiry, never direct purge. |

Old live history remains reachable via activity=all, appropriate archive filters,
Get/Events and explicit resume. Trash defaults hidden; visibility=trash/all lists it
without age cutoff. Get returns tombstone status; Events requires include_trash.
Open/Fork/Append/Archive reject trash: Restore first. Reads never silently restore.

Activity uses trustworthy `updated_at`: meaningful accepted writes or successful
explicit resume after ownership/projection/policy checks. Resume stores last_resumed_at
in a small versioned control sidecar, not an event. Open alone, reads, retries/no-ops,
scans, cache rebuilds, backups, Restore and cleanup do not advance activity. Imported
timestamps, mtime and discovery time are not current activity; unknown legacy activity
stays unknown, visible and protected from automatic retention.

```text
live/archived -- Delete or authorized retention --> trash -- deadline --> expired trash
     ^                                               |                       |
     +-------------------- Restore ------------------+                 safe Clean
                                                                         |
                                                                   permanently removed
pin / owner / pending work / unknown state --> block destructive transitions
```

Checkout-local retention policy must not unexpectedly delete another workspace's
sessions. Automatic cleanup is restricted to the current workspace captured by
OpenStore unless there is explicit project-wide authorization. User-global policy may
be used, but a shared source alone does not authorize a broader scope. No new policy
service is needed; use existing configuration and explicit authorization boundaries.
Missing/untrusted/ambiguous policy blocks destructive cleanup; changes cannot silently
shorten existing trash deadlines. These are proposed logical rules, not implemented keys/flags.

Clean is bounded within its Store project and authorized workspace scope; IDs only
narrow scope, never grant project-wide authorization or follow parent/fork links.
Explicit project-wide cleanup requires separate authorization, not a broader List result.
DryRun writes nothing, repairs nothing and claims no lease; report blocked recovery.
Results include per-ID action/reason, recoverability/deadline, verified byte estimates
(or unknown), truncation and partial outcomes on cancellation/failure.

Confirm previews with only selected returned IDs; previews reserve/authorize nothing.
Under writer exclusion, execution rechecks policy, prefix/control/lifecycle revisions,
tombstones and owners. Changed candidates skip/report, never expand or substitute.
Scheduled cleanup needs its own authorization; durable retry generations remain internal,
without client plan tokens, receipts or operation IDs.

Protect pins, idle writers, background work, pending delivery/approval, unfinished/unknown
execution/activity, corruption and unsupported schemas. Coordinate fork/import/backup and
delivery barriers or skip; reconcile crashed work before aging. Active dependencies matter,
not ancestry. Delete/Restore share applicable guards; no cascade or subtree transaction.
Trash resides in the stable project partition on the log/asset filesystem, not workspace/cwd.
Lifecycle metadata retains identity, inventory, validated prefix, authorization/deadline and
restart progress. Fence discovery/Open/stale handles before moves. Multi-file moves are
not atomic: incomplete transitions stay protected until recovered/safely rolled back;
readers never see partial history.
Restore validates content/references and original association; missing/corrupt/purge-started
content fails. ID/path collisions never overwrite, merge or allocate new IDs. Publish original
identity/history/archive/pin state completely, without runtime authority; retries distinguish
restored generations from later deletions. Clean alone permanently removes expired trash:
durably mark unavailable, restartably reclaim verified owned files without resurrection,
and retain lifecycle evidence until completion.

Cache eviction coordinates readers/writers. Asset GC scans **all retained history**, including
pre-compaction/archive/pin/trash, and protects staging/publication/backups. Only verified
temporary/orphan files past a grace period (e.g. 24 hours) qualify; recheck references before
unlink. Incomplete scans, unknown owners/formats or ambiguous references mean skip.
Logs, referenced assets and live lease files are not garbage.
Run bounded cleanup after interactive readiness/at idle; no running CLI means no daemon.
Expiry means eligibility at the next safe run. Disk pressure offers warnings, preview/export
and explicit choices, never shorter deadlines or bypassed protections.

## Rollout and validation

1. Remove SQLite experiment code and dependencies; retain file metadata, explicit sync,
   read-lock improvements and their tests. Preserve current native codec/truncate behavior, without a backend switch.
2. Define SDK-aligned batch framing, ownership, recovery and cache validation. Fault-test
   write/sync/rename/unlink boundaries, partial batches/moves, ambiguous commits and backups.
3. Implement registration, typed adapters and projections over files. Test TUI/headless/ACP,
   hooks/inspector and the three distinct reads: summaries, event/history browsing, resume state.
4. Require approved, exclusive, retryable conversion with preserved originals/assets,
   provenance and old/new projector parity. Unsupported records block cutover; no silent
   partition merge or dual writes. Preserve/export new history before rollback after new-format writes.
5. Measure before optimizing: incremental summaries, narrow locks, replay allocations and
   optional checkpoints. Backend test success alone does not prove the target contracts.

Validation must cover identity/worktree/config isolation and trust; all 18 events without
replay side effects; native/provider round trips; retries versus intentional repeats;
delegation/fork independence and late callbacks; repeated/audited/stale compaction;
fixed-prefix browsing; current-policy resume; retention defaults, pin/unknown guards,
preview races, interrupted trash/restore/purge, GC and consistent backups.

Remaining implementation choices: relocation/reconciliation UX, registration file format,
final summary/payload types, SDK batch integration and conversion bookkeeping.
Git common-directory identity, exact non-Git cwd and current-checkout project configuration
are settled. SQLite or a SQLite index over files requires a separate decision and new evidence.

## Historical performance — 2026-11-07

These are deferred-experiment measurements, not target guarantees or new benchmark results.
Apple M5 / 32 GiB, darwin/arm64, Go 1.26.9, modernc.org/sqlite v1.44.3, GOMAXPROCS=4.
Values are medians of three benchmark-run averages; not P95/P99 or cold-disk latency.
Fixture setup is excluded; OS caches were not flushed. JSONL appends explicitly Sync;
changed Save already syncs. SQLite uses WAL, synchronous=FULL, fullfsync=ON and
checkpoint_fullfsync=ON on every connection to match Go's macOS F_FULLFSYNC boundary.
Earlier measurements without those settings are excluded.

| Workload | JSONL ms/op | SQLite ms/op | Interpretation |
| --- | ---: | ---: | --- |
| San durable application append | 3.993 | 4.043 | Similar; more SQL allocations |
| Native append to 100-message context | 4.007 | 4.953 | About 24% slower |
| San replay, 1,000 messages | 2.947 | 3.534 | About 20% slower |
| List 100 sessions, warm summaries | 0.225 | 0.065 | About 3.5× faster |
| List after memory-cache loss, existing JSONL disk summaries | 1.490 | 37.900 | About 25× slower; SQLite lacks persisted product summaries |
| List with all summaries invalidated | 44.386 | 37.271 | About 16% faster |

Lists have 100 messages/session. Replay at 1,000 messages allocates about 2.27 MB/op
(JSONL) versus 3.80 MB/op (SQLite). Same-flags stripped binaries are **82,744,498 vs
86,546,162 bytes**, about 3.80 MB extra even when JSONL is selected at runtime.

Historical conclusion: keep JSONL, not a universal speed claim. Unequal persisted
summary support limits listing comparisons; optimize summaries, replay allocations and
per-record commits before reconsidering storage. SDK Save/Fork are atomic, while San
compound compaction/Fork still use existing multi-call protocols.

Any future comparison must use identical native fixtures/assets and durability, separate
warm/cold summaries, browsing and recovery, and report distributions, allocations, size and
contention across batches/concurrent writers. Existing benchmarks do not establish automatic
import, target-ID conversion, checkpoints, full runtime integration or power-loss certification.
