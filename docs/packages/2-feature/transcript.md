---
package: github.com/genai-io/san/internal/transcript
layer: feature
---

# transcript

Owns the native append-only event log and its derived projections.

## Purpose

`internal/session` writes messages and product state here; the inspector reads
the same records. The log is authoritative. This package owns message ancestry,
compaction boundaries, writer leases, crash recovery, and per-session summaries.

## Contract

The package exposes a concrete `*Store`, plus `Record`, `Transcript`, and
`Summary` values. SDK `ai.Message` and `ai.Schema` payloads stay native.

```go
func NewStore(baseDir, projectID string) (*Store, error)
func (s *Store) Claim(id string) (func(), error)
func (s *Store) Start(ctx context.Context, cmd StartCommand) error
func (s *Store) Append(ctx context.Context, rec Record) error
func (s *Store) Position(ctx context.Context, id string) (string, int, int, error)
func (s *Store) Load(ctx context.Context, transcriptID string) (*Transcript, error)
func (s *Store) LoadHistory(ctx context.Context, id string) (*Transcript, error)
func (s *Store) List(ctx context.Context, projectID string, opts ListOptions) ([]Summary, error)
func (s *Store) Fork(ctx context.Context, cmd ForkCommand) error
func (s *Store) Delete(ctx context.Context, transcriptID string) error
func (s *Store) Flush() error
func Project(records []Record) (*Transcript, error)
```

## Internals

- `store.go` durably appends before advancing caches. Message IDs name immutable
  content; a replacement selects an ordered chain with unique, nonempty IDs.
  Replaying the same IDs does not copy history or erase compaction ancestry,
  including when the session adapter has hydrated blob-backed output.
- `projector.go` follows the selected message-parent chain. Resume stops at the
  latest compaction boundary; history follows it back through older messages.
  Session ancestry uses a separate `parentSessionId`.
- `recovery.go` refreshes derived caches on every successful writer claim, so
  leaf IDs, inference/exchange counters, state, and tail recovery reflect
  intervening writes by other processes. Reads ignore an incomplete final JSON
  fragment; an append repairs it. Terminated corruption remains an error.
- Each `transcripts/<id>.jsonl` has a derived `<id>.jsonl.meta`, validated against
  log size and modification time. Listing scans logs directly. Missing or
  corrupt summaries rebuild from the log; there is no shared index to overwrite.
- Schema version `2` is declared on `session.started`. Old formats and migration
  paths are unsupported. Fork preserves timestamps and rewrites attachment
  references to the child's blobs; the session adapter copies those blobs.

## Lifecycle

`NewStore` binds a project directory. Methods serialize cache access with the
store mutex. `Claim` takes a cross-process file lock; its returned release
function ends the writer lease. Callers must hold that lease while writing a
conversation. `Flush` refreshes derived summaries; it is not a durability barrier
for message records, which are already synced on append.

## Tests

- `store_test.go` covers native event shapes, append deduplication, fork, and load.
- `recovery_test.go` covers torn tails, sequential and competing writer leases,
  cache refresh across stores, summary independence, and old-format rejection.
- `projector_test.go` covers message ancestry and compaction projection.
- Session and app tests cover hydrated resume, error retry, and inactive
  compaction through the actual writer/runtime boundaries.

## See Also

- Code: `internal/transcript/`
- Product adapter: [session](session.md)
- Running conversation: [agent](agent.md)
- Replay UI: [inspector](inspector.md)
- [Dependency rules](../../reference/dependency-rules.md)
