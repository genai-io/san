---
package: github.com/genai-io/san/internal/transcript
layer: feature
---

# transcript

Owns the native append-only event log and its derived projections.

## Purpose

`internal/session` writes messages and product state here; the inspector reads
the same records. The log is authoritative. Storage — the append-only file,
writer leases, crash recovery, listing and deletion — is sdk-go's
`pkg/session`: messages are its conversation (message and reset lines), and
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

- `store.go` writes through the claimed `session.Writer`: a message extends the
  conversation and is saved, a replacement is saved whole (a reset and its
  messages), and a `session.compacted` saves the conversation from its summary
  on. Message IDs name immutable content; a replacement holds unique, nonempty
  IDs, and replaying the same IDs writes nothing.
- `read.go` reads the log back as `Record`s: a message as `message.appended`, a
  reset as an empty `conversation.replaced`. A record's ID is its position in
  the log.
- `state_patch.go` applies typed partial updates and removes unchanged fields.
- `projector.go` is linear. Resume starts over at each reset; history keeps
  every message once, in the order it first appeared. Session ancestry uses
  `parentSessionId`.
- `recovery.go` claims a session through `session.Store.Write` and refreshes
  derived caches on every claim, so inference/exchange counters and state
  reflect intervening writes by other processes.
- Each `transcripts/<id>.jsonl` has a derived `<id>.jsonl.meta`, validated against
  log size and modification time. Missing or corrupt summaries rebuild from the
  log.
- Schema version `4` is declared on `session.started`; a log that does not open
  with it is rejected without migration. Fork writes the source's records into
  the child with attachment references rewritten to the child's blobs; the
  session adapter copies those blobs.

## Lifecycle

`NewStore` binds a project directory. Methods serialize cache access with the
store mutex. `Claim` takes the session's writer, a cross-process lease; its
returned idempotent release function closes it. Callers hold that lease while
writing a conversation; a write outside one takes the writer for that write.
A message or replacement is synced before returning; other records are synced
by the next one, or by release. Delete refuses a held session. Session and
attachment identifiers must be single path components.

## Tests

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
- [Dependency rules](../../reference/dependency-rules.md)
