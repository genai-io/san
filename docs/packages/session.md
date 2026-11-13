---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Owns canonical project/session identity, durable typed events, native conversation
projections, assets and retention. TUI, print, ACP, hooks, utility completions,
subagents and the inspector use this one backend. The application selects the
session; `agent.Runtime` owns execution. There is no legacy reader, fallback,
dual write or automatic conversion of older session files.

## Entrypoints and ownership

- `OpenStore(ctx, startDir)` resolves the project. `Store.Create`, `Open` and
  `Fork` return a `Session` with an exclusive writer lease. Close every opened
  session before closing its store; closing a handle does not end the conversation.
- `Store.Read(ctx, id)` returns an immutable `Projection` without acquiring a
  writer. `Get`, `List`, `Events`, `Attrs`, `ContextRequest`, `InferenceRequest`
  and `CommittedBatches` support inspection. Overview caches are disposable;
  `Read`, request reconstruction and raw committed-batch reads do not modify logs.
- `Session.Append` is the event write boundary. It assigns envelopes, validates
  provenance and commits one atomic batch. `Snapshot` returns committed state;
  `ID`, `Path`, `Err` and `OwnedBy` expose handle identity and health.
- `StartInference` persists the effective request before dispatch;
  `Inference.Finish` commits accepted results and accounting together.
- `Recorder` adapts core inference/tool/output admissions and observations.
  Install all admissions with `RecordEvent`: observations alone cannot fence
  effects. `RecordPermission`, `RecordHook`, `RecordState` and
  `RecordCatalogues` record product facts, not a second conversation snapshot.
- `Completion` owns utility-call attempts until the domain accepts or discards
  their results. It captures the original writer and must not be redirected when
  the displayed session changes.

## Project identity and configuration

Inside Git, the canonical Git common directory identifies the project; checkout
root identifies its workspace. Linked worktrees share sessions, while separate
clones and submodules do not. Outside Git, the exact canonical startup directory
is the project and workspace; there is no ancestor project lookup. Paths are
absolute and symlink-resolved. A moved repository gets a new identity.

Settings, instructions and trust remain checkout-local and resolve using current
credentials. A session fixes project, workspace and initial cwd at creation;
`state.execution_cwd` records later execution state without changing project
ownership. Hosts reject implicit directory switching on resume. Saved bypass
mode never grants permission that current startup policy did not grant.

## Storage

```text
~/.san/projects/<hash(kind + key)>/
  project.json
  sessions/<sid>/
    events.jsonl                 sdk-go committed batches of typed events
    events.jsonl.lock            exclusive writer lease
    attrs.json                   optional renamed title, archive and pin
    blobs/<sha256>               immutable standalone request copies
    blobs/images/               pasted image assets
    blobs/tool-result/<call-id>  complete paged tool output
  cache/<sid>.json               disposable overview
  trash/<sid>/                  whole deleted session directory
```

Project `.locks/<sid>` paths coordinate short append/attrs/directory/GC mutations;
they are not authoritative conversation facts. Request blobs sync before the
referencing event commits. SDK transport recovery remains in sdk-go; product
validation and projection remain in `session`.

An incomplete transport suffix is ignored by readers, not repaired by them.
Unsupported versions, invalid references and complete corruption block validated
resume rather than being silently skipped. Files from earlier releases under
`transcripts/` or cwd-hash partitions remain untouched and are not discovered.

## Event and request flow

The thirteen families are `session.create`, `session.update`, `message.append`,
`context.compact`, `inference.start`, `inference.finish`, `permission.decide`,
`tool.finish`, `instructions.update`, `toolset.update`, `skills.update`,
`hook.finish` and `annotation.add`. Event identity is `(session, seq)`; `source`
links provenance, not executable instructions.

Native SDK messages retain content ordering, images, signatures, encrypted
reasoning and tool IDs. Messages are append-only; `context.compact` replaces only
active context with an accepted summary plus later messages. Full history keeps
precompaction messages and the summary. Foreign provenance travels with its
acceptance evidence so later deletion of a child does not break parent replay.

Queued input is durable at native acceptance: `message.append {accepted: true}`
retains the actual foreign or utility provenance. Its later consumption appends
exactly the same native message IDs/content with a local source back to that
acceptance. `Messages()` and `ActiveMessages()` deduplicate these lifecycle facts,
ordering consumed messages before the still-pending tail. Compaction retains
pending accepted input rather than dropping or prematurely consuming it.

Admission compares the SDK-prepared effective request with projected instructions,
tool schemas and repaired active context. Matching requests record conversation
input; other requests store an exact standalone copy in `blobs/`. Generation
settings retain native tool choice, schema, cache and sampling options. Historical
request reconstruction uses the selected committed prefix, not present settings.

A permission decision commits before tool admission, and only an actual allow
permits execution. `tool.finish` records a call that ran; denied calls do not get
fabricated finishes. Raw tool display details stay separate from filtered
model-facing result content. Observer/persistence failures block later admissions.

Utility scopes cover judge, hook, autopilot, group, compaction, recall, BTW, recap
and suggestion calls. Failed attempts close before retries. Accepted outputs join
their final inference finish atomically; stale or canceled work finishes accounting
without publishing a conversation result. Queued and detached work retains its
original owner. Child hook factories receive the actually selected provider/model;
a same-engine fallback is rebound to that captured owner. TUI shutdown joins hook
work before closing its writer. Grants follow successful persistence; external group operations
are not rolled back by a later write failure.

## Resume, display and application state

`Runtime.Resume` validates its opened owner and execution cwd, seeds
`Projection.ActiveMessages()` and reconciles current catalogues with cause
`resume`. It returns unfinished operations without retrying them or inventing
results. Hosts resolve saved provider/model against current connections, reset
session permission grants and read-before-write caches, and report interruptions
as reconstructed UI status, not new annotation events on every open.

`Store.ChatRows(projection)` renders full history, explicit annotation anchors,
raw structured tool details and hydrated paged output. Unanchored annotations
stay at their event position. Package `ChatRows(messages)` is the reusable native
message conversion. Image source notes injected for the model stay persisted but
are filtered from display; image/text order stays intact. Display never rewrites
the persisted native messages.

`PersistToolResult` writes the complete output beneath the selected session;
ResultPager's marker remains `blobs/tool-result/<sid>/<call-id>`. Forked native
messages retain that marker, but hydration reads only the selected session's own
copied assets. `ImagesDir` exposes its named image directory. Named assets are
separate from digest-addressed request blobs and are not blob-GC candidates.

`session.update` holds config and product state, never a replacement message
chain. Autopilot policy contains full native settings with mission progress kept
separately. Todos retain native tracker descriptions, metadata, dependencies,
owners and timestamps. User-renamed titles live in attrs and override generated
state titles. `RecordState` and `RecordCatalogues` write nothing when unchanged.

## Fork, organization and retention

`Fork` copies a validated event prefix into an independent session, retaining
native message IDs, provenance and unfinished operations. It changes creation
identity, clears parent/group links, verifies referenced request blobs and copies
named display assets. A fork is not a child; children have their own creation
parent link. Deleting the source does not invalidate the fork.

`SetAttrs` uses short metadata coordination rather than the writer lease.
`Delete` requires a closed, unpinned session and atomically moves its whole
directory to trash. `Restore` never overwrites a live ID. Trash waits seven days
before purge. Recent lists default to thirty days; pins stay visible and block
retention. Automatic retention is off unless requested through `Clean` options.
`Clean` supports scope, dry run and a shared action budget; it preserves unknown
assets and collects only eligible stale staging/unreferenced digest blobs.

## Files and tests

The implementation is grouped by capability: `store.go` / `session.go`,
`event_model.go`, `event_validation.go`, `event_projector.go`, `event_storage.go`,
`event_query.go`, `read.go`, `inference.go`, `recorder.go`, `completion.go`,
`display*.go`, `blob.go`, `attrs.go`, `overview.go`, `list.go`, `fork.go`,
`lifecycle.go`, `clean.go` and `project.go`.

- `typed_store_test.go`, `typed_api_external_test.go`: ownership, public API,
  atomic append, concurrency, lifecycle and foreign provenance.
- Event/storage/read tests: validation, corruption, immutable prefixes, writer-free
  reading, request reconstruction and no reader repair.
- Recorder/completion/permission/compaction tests: real SDK admission and
  accounting boundaries, cancellation, retries and injected write failures.
- `display_test.go`, `native_state_test.go`: images, signatures, previews,
  annotations, tool details, full history, native policy/tracker values.
- `tests/integration/session`, app/agent/subagent/ACP tests: host selection,
  restart, fork, permission isolation and native runtime wiring.

Run `go test -race ./internal/session` for focused validation; use the repository
CI commands for cross-package and dependency checks. See
[PROP-0005](../design/proposal-0005-session-management.md) and
[the completed cutover](../../notes/completed/session-cutover.md) for the design
and validation record.
