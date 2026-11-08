# PROP-0005: Session management and file-backed persistence

[中文](proposal-0005-session-management.zh.md)

## Status and scope

Draft — 2026-11-07. Design only: storage-neutral API over files/JSONL; no SQLite migration or backend-selection UI. Persist conversation changes, necessary execution/permission audit and non-reconstructible commentary—not a runtime log. Summaries/indexes are rebuildable.

| Delivery | Status |
| --- | --- |
| [Implementation #737](https://github.com/genai-io/san/pull/737) | Unmerged: project/worktree identity, checkout-local settings, schema-4 identity/cache upgrades; 18-event model/projector, private typed persistence and public bounded raw Events reader. No runtime cutover. |
| [SDK #105](https://github.com/genai-io/sdk-go/pull/105) | Merged at `4bc968c`: opt-in committed batches and inference/tool/output admission. Complete-line visibility is not proof of completed Sync; successful Commit acknowledges file Sync. |
| Existing persistence | SDK native records/truncate; Hooks/inspector retain JSONL integration; no legacy partition migration or dual-write. |
| Target below | Runtime event writes, complete producer integration/API, trash and retention remain gated; private persistence is not a format cutover. |

### Current slice versus target API

The API contracts, 18-event table and diagrams below describe the **target**.
The unmerged slice keeps runtime writes on schema 4; typed writes remain private.
Public `Store.Events` returns raw events (default 100, maximum 500 per page),
replaying the full pinned prefix on every page; bounded output is not bounded work.
Filters, lifecycle-generation binding and full runtime integration remain targets,
not current APIs. See the implementation docs in [#737](https://github.com/genai-io/san/pull/737)
for persistence, retry and creation limitations.

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

```text
creation: project_id + workspace + initial_cwd (fixed)
                                  |
resume: state.execution_cwd -------+ fallback for old history
                  |
                  +--> tools / settings / hooks / trust at execution location
                  `--> cwd may cross projects; ownership NEVER follows
```

Git workspace = checkout root; initial_cwd may be below it. Moves, reused paths or changed/missing Git metadata require explicit registration validation/reconciliation. Never silently transfer identity, merge legacy partitions or rewrite history. Association grants no trust; missing checkouts do not block inspection.

| Configuration boundary | Rule |
| --- | --- |
| Project settings | Current Git checkout root, not original main checkout; exact startup cwd outside Git. Tracked settings/instructions follow checkout/branch. |
| Sharing | User-global naturally shared; links opt-in. No mandatory shared root, copying/sync or fallback to another checkout; main checkout need not exist. |
| Existing loaders | Preserve config/MCP/plugin/skill/instruction path rules and applicable legacy local-source/trust compatibility. Sharing identity requires no redesign; not every loader normalizes subdirectories to checkout root. |
| Resolution/writes | Preserve user → project → local, compatibility sources, persona, field merges/provenance ([configuration](../reference/configuration.md)). Write one scope's fields, never merged output; coordinate processes and reject read-only sources. |
| Session overrides/resume | Subsequent requests only; current policy/credentials, never historical grants. Missing model/provider needs error or explicit replacement. No config/event transaction, secrets, security authority or full settings-tree copies. |
| Current limitations | Registration lock waits cannot be cancelled mid-wait; atomic publication ≠ power-loss durability; identical-path repository replacement is not automatically detected. |

```text
~/.san/projects/
  <cwd-hash>/                      legacy: separate, readable; no auto-move/import
  <project-id>/                   shared worktrees; no workspace directory layer
    project.json                  identity/lookup metadata, NOT config (implemented)
    transcripts/<sid>.jsonl        peer parent/child logs; parent_session_id links
    transcripts/<sid>.jsonl.meta   rebuildable summary
    blobs/{image,tool-result}/<sid>/
    trash/<sid>/                  PROPOSED generations/manifests/logs/assets
checkout / user scope             config stays with existing loaders
```

The internal catalog owns names/descriptions, registration and partition locations—not config/cwd. Registration scans rebuild lookup indexes, not identity. Reconciliation needs approval; project removal is blocked by sessions, trash or unfinished transitions. Session deletion never deletes checkouts or cascades by ancestry; trash recovery cannot assume atomic multi-file moves.

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

```text
OpenStore(dir) --> project-scoped *Store (internal registration; no public services)
  |-- Get/List ------------> summaries; no writer claim
  |-- Events --------------> fixed committed prefix --> History --> display pages
  |-- Create/Open/Fork ----> exclusive *Session
  |                            |-- Append/Compact --> atomic commit
  |                            |-- Snapshot -------> active committed projection
  |                            `-- Close ----------> drain + release (not session end)
  `-- Delete/Restore/Clean -> lifecycle metadata, not conversation events
```

Consumers may define narrow interfaces; final primitives/types align with sdk-go. **Proposed signatures below:** current Store has Create/Open/List/ListContext/Fork/Delete/Close; Open/Fork also return History, Append accepts one transcript.Record, Records exposes raw records, and Delete immediately removes history. Session.Close already exists.

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

Durable writes: Create/Fork → one session.create plus remapped inherited facts; Compact/audited Append → compaction.apply (+ known inference.finish atomically); Archive → state.update.archived_at under exclusive ownership (open writers use Append). State/config owners and runtime adapters use typed Append, not new Store verbs; settings files retain their owner. Reads/Open/Close emit no conversation events; successful explicit resume uses control metadata, deletion uses lifecycle metadata.

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

| Subagent boundary | Contract |
| --- | --- |
| Dispatch | Reserve child ID; create/claim and validate parent/task/child ownership before dispatch. Each continuation gets a new start. |
| Acceptance | Parent stores coordination/accepted content, not child streams. Known result + finish commit together; no invented empty output. Independent content/assets, fresh receiver-local native IDs, aligned source_message_ids; preserve provider fields/tool identities. |
| Retry/delivery | Trusted delivery keeps stable acceptance event/message IDs across retries/restarts; changed content/source/receiver conflicts, intentional deliveries get new IDs. Acknowledge after acceptance; only inference input IDs prove consumption. Transport/group inbox owns routing/retry; no session queue, cross-session transaction or exactly-once execution. |
| Runtime | Late results go to original writer, not selected UI/fork. Source deletion affects tracing, not retention. External roster/inbox ownership unchanged: resume reconciles/reclaims hints, switch releases membership, fork clears hints. Replay recreates no processes/callbacks/child ownership; live continuation revalidates. |

```text
fixed nonempty source prefix              staged fork (protect source until publish)
seq 1: create E1 -----------------------> create F1 (source initial config)
seq 2..N: E2..EN -----------------------> F2..FN (fresh global event IDs; same seq)
local refs: E* -------------------------> F* (envelope AND payload)
foreign refs / native message IDs / seq anchors ----> unchanged
assets --------------------------------> independent copies + rewritten refs
inherited replay --> destination adjustments + clear group_hint --> publish whole log
```

Remap **all** local event references: source_id, operation_ref, result_event_id, input/effect references; missing required mappings fail. Preserve historical patches, append only necessary child-local changes. Historical delegation neither reparents children nor transfers delivery mappings or execution/permission/group authority. Fresh replay must equal Fork's result and resolve content after source deletion; unfinished attempts never restart.

**Compaction:** active context → native summary + ordered retained messages; index summary in History without another message event. Preserve history/config/state/assets (storage lossless ≠ summary lossless). Serialize input capture/start before generation outside commit lock; shared Append/Compact validates pre-batch tail, summary identity, same-session existing unique references, active order and tool-call/result consistency. Earlier active summaries may be retained.

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

**Proposed local-file requirements**, not ordinary JSONL guarantees or network-filesystem support. SDK owns generic native codecs/ownership/commit primitives; San owns policy.

| Boundary | Requirement |
| --- | --- |
| Atomic batch | One write/multiple lines ≠ atomic. Versioned verifiable framing binds identity, expected prior tail, seq/count/integrity; SDK integration choice, not an event type. Validate/prepare projection → complete flush/fsync → acknowledge/publish; no partial reads. |
| Recovery | Only validated committed prefixes; ignore incomplete tail batches, repair exclusively before writes. Interior corruption fails, never skips. Stable IDs/content reconcile ambiguous commits; cancellation ≠ rollback. Unknown state-affecting versions block resume. |
| Ownership | Fenced per-session cross-process writer, including idle handles; no timestamp-only leases or unlinking live locks. No inference/tool/approval inside commit critical section. Death releases ownership, not proof of external completion or exactly-once execution. |
| Publication/backup | Publish/sync assets before references. Create/Fork expose only complete validated staging; discovery ignores staging. Back up committed prefixes, registrations, authoritative control/lifecycle metadata, trash and assets together; separate copies of changing logs/blobs are inconsistent. |

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

| Cleanup boundary (proposed, not implemented keys/flags) | Required behavior |
| --- | --- |
| Authorization | Automatic cleanup: OpenStore's captured workspace unless explicitly authorized project-wide. Checkout-local policy cannot delete other workspaces; user-global source/broader List/IDs grant no broader authority. Existing config/authorization suffices; missing/untrusted/ambiguous policy blocks destruction, policy changes cannot shorten trash deadlines silently. |
| Bounds/preview | Store project ∩ authorized workspaces ∩ optional IDs; never follow parent/fork links. DryRun writes/repairs nothing, claims no lease, reports blocked recovery. Finite Limit default/maximum; per-ID action/reason, recoverability/deadline, verified bytes or unknown, truncation and partial cancellation/failure results. |
| Confirmation/race | Submit only selected returned IDs; preview reserves/authorizes nothing. Under writer exclusion recheck policy, prefix/control/lifecycle revisions, tombstones/owners; changed candidates skip/report, never substitute/expand. Scheduled cleanup needs separate authorization; retry generations internal, no client tokens/receipts/operation IDs. |
| Guards | Pins, idle writers, background work, pending delivery/approval, unfinished/unknown execution/activity, corruption/unsupported schemas. Coordinate fork/import/backup/delivery barriers or skip; reconcile crashed work before aging. Active dependencies, not ancestry; Delete/Restore share applicable guards, no cascade/subtree transaction. |
| Trash transition | Stable project partition on log/asset filesystem, not workspace/cwd. Metadata retains identity, inventory, validated prefix, authorization/deadline/restart progress. Fence discovery/Open/stale handles before moves; multi-file moves are NOT atomic. Protect incomplete transitions until recovery/safe rollback; no partial history. |
| Restore | Validate content/references/original association; missing/corrupt/purge-started fails. Collisions never overwrite/merge/allocate new IDs. Publish original identity/history/archive/pin completely without runtime authority; distinguish restored generations from later deletion on retry. |
| Permanent removal | Clean alone purges expired trash: durable unavailable marker → restartable reclaim of verified owned files → retain lifecycle evidence until complete; no resurrection. |
| GC/cache eviction | Coordinate readers/writers. Scan ALL retained history (pre-compaction/archive/pin/trash); protect staging/publication/backups. Only verified temporary/orphan files beyond grace (e.g. 24h), recheck references before unlink. Incomplete scans/unknown ownership or format/ambiguous references → skip. Logs, referenced assets and live leases are not garbage. |
| Scheduling/pressure | Bounded after interactive readiness/at idle; no CLI → no daemon. Expiry means next safe run eligibility. Disk pressure offers warnings/preview/export/explicit choices, never shorter deadlines or bypasses. |

## Rollout and validation

```text
legacy schema 4 + SDK codec/truncate --> existing readers (originals preserved)
                  |
                  v
SDK batches + fenced ownership + durable admission + recovery/cache fault tests
                  |
validated adapters --> old/new parity gate --> approved exclusive retryable conversion
                  |   context/history/native IDs/assets/inspector incl. replacements
                  `-- unsupported/missing facts --> BLOCK; no fabricated/discarded facts
conversion --> new format (no dual writes / silent partition merge)
rollback after new writes --> preserve/export new history FIRST
```

Remove SQLite experiment/dependencies, retaining file metadata, sync, read-lock improvements/tests and native behavior. Test TUI/headless/ACP, hooks/inspector and all three read paths. Fault-inject write/sync/rename/unlink, partial batches/moves, ambiguous commits and backups. Measure summaries/locks/replay allocations before checkpoints; backend tests alone do not prove target contracts.

Validation: identity/worktree/config isolation/trust; all 18 events without replay effects; native/provider round trips; retries vs intentional repeats; delegation/fork independence/late callbacks; repeated/audited/stale compaction; fixed-prefix browsing/current-policy resume; retention defaults/pin/unknown guards, preview races, interrupted trash/restore/purge, GC/backups.

Open choices: relocation/reconciliation UX, registration format, final summary/payload types, SDK batch integration/conversion bookkeeping. Settled: Git common-directory identity, exact non-Git cwd, checkout-local config. SQLite (including an index) needs a separate decision/new evidence.

## Historical performance — 2026-11-07

Deferred experiment, **not target guarantees/new results**: Apple M5 / 32 GiB, darwin/arm64, Go 1.26.9, modernc.org/sqlite v1.44.3, GOMAXPROCS=4. Median of three run averages, not P95/P99/cold-disk latency; fixtures excluded, OS caches not flushed. JSONL append explicitly Syncs; changed Save already syncs. Every SQLite connection: WAL, synchronous=FULL, fullfsync=ON, checkpoint_fullfsync=ON, matching Go macOS F_FULLFSYNC. Earlier unmatched measurements excluded.

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
