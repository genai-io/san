---
package: github.com/genai-io/san/internal/transcript
layer: feature
---

# transcript

Owns the native append-only events and their derived projections.

Transcripts use sdk-go's JSONL file store. The
[session management proposal](https://github.com/genai-io/san/pull/736)
describes future changes, not the current storage contract.

## Purpose

`internal/session` writes messages and product state here; the inspector reads
the same records. The events are authoritative. Storage — the append-only file,
writer leases, crash recovery, listing and deletion — is sdk-go's
`pkg/session`: messages are its conversation (message and truncate lines), and
every other `Record` is written whole as an application record's data. This
package owns san's record types, their projection, and per-session summaries.

## Contract

The package exposes a concrete `*Store`, plus `Record`, `Transcript`, and
`Summary` values. SDK `ai.Message` and `ai.Schema`, and domain `todo.Item`
payloads stay native.
State patches are reduced to actual changes under the append mutex; unchanged
fields produce no redundant record. `StatePatch` uses typed pointers for title,
prompt, mode, autopilot, group, and tasks; nil means unchanged, an explicit empty
value clears. Task metadata is copied into its durable JSON form before caching.

```go
func NewStore(baseDir, projectID string) (*Store, error)
func (s *Store) Exists(ctx context.Context, id string) (bool, error)
func (s *Store) Close() error
func (s *Store) Claim(id string) (func() error, error)
func (s *Store) Start(ctx context.Context, cmd StartCommand) error
func (s *Store) Append(ctx context.Context, rec Record) error
func (s *Store) Position(ctx context.Context, id string) (string, int, int, error)
func (s *Store) Load(ctx context.Context, transcriptID string) (*Transcript, error)
func (s *Store) LoadHistory(ctx context.Context, id string) (*Transcript, error)
func (s *Store) List(ctx context.Context, projectID string, opts ListOptions) ([]Summary, error)
func (s *Store) Fork(ctx context.Context, cmd ForkCommand) error
func (s *Store) Delete(ctx context.Context, transcriptID string) error
func (s *Store) Records(ctx context.Context, id string) iter.Seq2[Record, error]
func Project(records []Record) (*Transcript, error)
```

## Internals

- `store.go` writes through the claimed `session.Writer`. Save compares the
  conversation with its previous encoding, truncates to the unchanged prefix
  when needed, and appends the changed suffix. A `session.compacted` saves the
  conversation from its summary on. San requires unique, nonempty message IDs
  in replacements and treats those IDs as immutable content identities.
- `read.go` reads messages as `message.appended` and truncates as
  `conversation.replaced` carrying the retained prefix, which may be nonempty.
  A record's ID is its position in the log.
- `state_patch.go` applies typed partial updates and removes unchanged fields.
- `projector.go` is linear. Resume replaces its active conversation at each
  replacement; history keeps every message once, in first-appearance order.
  Session ancestry uses `parentSessionId`.
- `recovery.go` claims a session through `session.Store.Writer` and refreshes
  derived caches on every claim, so inference/exchange counters and state
  reflect intervening writes by other processes.
- JSONL `transcripts/<id>.jsonl` has a derived `<id>.jsonl.meta`. SDK `Stat`/`Info`
  supplies per-session size and modification time for cache validation.
  Listing reuses SDK metadata rather than statting twice. Cache format version 1
  rebuilds older summaries once without rewriting logs.
- New `session.started` payloads include optional `projectId` and `workspace`.
  Store supplies project identity; projection, metadata and summaries preserve both
  fields. Missing legacy fields remain unknown, and same-project forks retain them.
  These are storage/execution identity, not configuration authority: linked Git
  worktrees share a project ID while loading their own checkout's project settings.
  No settings snapshot or registry field overrides current trust checks.
- Schema version `4` is declared on `session.started`; a log that does not open
  with it is rejected without migration. Fork writes the source's records into
  the child with attachment references rewritten to the child's blobs; the
  session adapter copies those blobs.

## Typed batch transport (unmerged slice)

`batch.go` exposes `CommitBatch`, `BatchTail` and read-only `Batches` for the
private typed persistence path in [session](session.md). Commit and tail require
an already claimed writer (`ErrUnclaimed` otherwise); commit invalidates legacy
cached projections even on failure because bytes may have been written. `Batches`
yields whole validated SDK batches without claiming or repairing the log.
Successful SDK Commit acknowledges file Sync; visible complete lines alone do not.
San stores one version-1 `san.events` application record per typed batch, including
retained validation evidence; product validation and retry state belong to session.

`CheckLegacyFormat` probes the first record before legacy callers claim a writer.
Batch format is refused with `ErrEventFormat`, not flattened, converted or repaired
as schema 4. This probe is not full-log validation. Typed open separately validates
complete product facts before SDK tail recovery; read-only queries never repair.
Committed corruption fails rather than being skipped; unfinished transport tails
are handled by SDK recovery under writer ownership.

Ordinary runtime writes and reads retain the schema-4 contract above: no dual-write
or runtime cutover. The public `Store.Events` reader in session returns bounded raw
pages but replays the full pinned prefix on every page, so output bounds do not
bound replay cost or memory. Typed creation is private, not crash-atomic and not
an idempotent create retry API. Its initial existence check refuses existing files,
including abandoned empty files, but cannot distinguish an empty file concurrently
created and abandoned after that check from its own new file. Closing this race
requires a future SDK exclusive-create primitive. See session for retained evidence
and batch retries.
`batch_test.go`, `batch_format_test.go` and `batch_legacy_test.go` cover transport,
format refusal and legacy isolation.

## Lifecycle

`NewStore` binds a project directory. Writer/projection caches
use the store mutex. Listing has a separate summary-cache mutex, and read-only history
replay does not acquire the writer-cache mutex. This avoids blocking appends behind a
cold listing; writer-cache operations are still serialized, not per-session sharded.
`Claim` takes the session's writer, a cross-process lease; its
returned idempotent release function closes it. Callers hold that lease while
writing a conversation; a write outside one takes the writer for that write.
JSONL changed conversation saves sync before returning; application appends sync on
a subsequent changed save or writer close, not another application append or no-op
save. SDK `Writer.Sync` provides an explicit durability boundary for pending
application records. San's compaction and fork flows remain multi-call operations.
Delete refuses a held session. Identifiers must be single path components.
Close all leases before `Store.Close`; a busy close leaves owners intact.
`TranscriptPath` returns the session's JSONL path.

## Tests

- `jsonl_lifecycle_test.go` covers native fidelity, history/replacement, state,
  metadata invalidation, identity validation and lease handoff.
  `store_concurrency_test.go` checks read/cache lock separation.
  `jsonl_bench_test.go` measures explicitly synced application writes, replay,
  warm/persisted/rebuilt summaries and parallel read-only history.
- `store_test.go` and `events_shape_test.go` cover the on-disk shapes, append
  deduplication, compaction, fork, and load.
- `recovery_test.go` covers writer leases, cache refresh across stores, summary
  independence, and schema validation.
- `projector_test.go` covers resume and history across a reset.
- `fork_safety_test.go` covers occupied destinations and cross-store writer leases.
- Session and app tests cover hydrated resume, error retry, and inactive
  compaction through the actual writer/runtime boundaries.

## See Also

- Code: `internal/transcript/`
- Product adapter: [session](session.md)
- Running conversation: [agent](agent.md)
- Replay UI: [inspector](inspector.md)
- [Dependency rules](../reference/dependency-rules.md)
