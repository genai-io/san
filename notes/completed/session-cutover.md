# Session cutover plan

Implements the unified native `sessions/<sid>/` backend from
[PROP-0005](../../docs/design/proposal-0005-session-management.md). Production hosts
now target canonical `session.Store` / `Session`; the old persistence package,
records, projector and constructors have been removed. This is an intentional
format break: no fallback, automatic conversion or dual write is provided.

## Delivery status

1. **Storage and public API implemented.** Thirteen typed event families,
   positional provenance, immutable projections, standalone request blobs, attrs,
   overview caches, listing, atomic fork, trash/restore and scoped cleanup.
   `Read`, request reconstruction and committed-batch inspection do not need a
   writer or repair files.
2. **Native producer adapters implemented.** Core admissions fence model/tool
   execution; final permission facts precede dispatch. Interpreted hook outcomes,
   accepted utility results and compaction boundaries share atomic finishes with
   their inference accounting. Retry attempts retain individual accounting.
3. **Resume and child execution implemented.** Active native context and current
   catalogues restore without replaying effects. Unfinished calls are reported,
   never completed synthetically. Child identity precedes execution; foreign
   provenance retains acceptance evidence independently of surviving child files.
4. **Unified consumer switch complete.**
   TUI, print, ACP, hooks, utility completions, subagents and inspector use the
   native APIs. Hook-facing paths name `events.jsonl`; captures remain bound to
   their original writer generation. Permission grants reset on replacement,
   including a new handle for the same ID. Saved execution directories are
   validated without implicit cwd switching. Saved model/provider resolve against
   current credentials; bypass is never granted by persisted history.
5. **Legacy deletion implemented.** No older-format reader, metadata alias,
   constructor, partition lookup or separate persistence package remains. Old
   files on disk are ignored and left untouched. Format-specific tests have been
   replaced by native event/request/provenance assertions rather than runtime
   compatibility shims.

## Native product-state and display checks

- Queued native acceptance persists `message.append {accepted: true}` with its
  actual provenance; exact-ID consumption appends a local reference to acceptance.
  Projections deduplicate, order consumed messages before pending input, and keep
  the pending tail through compaction.
- Child hook factories use the selected provider/model, rebind same-engine
  fallbacks to the captured owner, and TUI shutdown drains hooks before writer close.
- Autopilot policy retains full settings; mission progress is separate state.
  Native tracker items preserve metadata, dependencies, descriptions and times.
- Renamed titles are attrs, distinct from generated titles. Read/display state
  uses full persisted history; runtime context uses only active messages.
- Image order/source-note filtering, provider signatures, raw tool details and
  full-result hydration are display projections that never rewrite native messages.
- Pager markers stay stable while files live within each session's blobs tree.
  Fork copies named image/tool-result assets separately from immutable request
  blobs. Named assets are not digest-GC candidates.
- Reconstructed unfinished-operation notices are UI-only, not duplicate durable
  annotations on each resume.
- Inspector uses native read/request/batch APIs. It preserves raw SDK batches,
  checks SSE prefix identity and keeps unvalidated diagnostics separate from
  accepted events.

## Validation

The unified tree passes the repository gates without compatibility aliases,
weakened rules or skipped tests:

- `go test -race -p 4 -timeout 120s ./...`
- `GOFLAGS=-p=4 make cover`
- `make lint test-inspector-ui build` (nine Inspector UI tests)
- `go mod verify`
- `git diff --check`

## Deferred follow-ups

- Event pages still read the required prefix; efficient offset pages need sdk-go
  `Store.BatchesFrom` support.
- Retention age is not a setting yet; `Clean` takes it as an option.
- The canonical product API stays in `internal/session`; generic batch framing,
  writer locking, request preparation and transport recovery remain in sdk-go.
