---
package: github.com/genai-io/san/internal/session
layer: feature
---

# session

Persists conversation transcripts to disk, generates session IDs, and
exposes save/load/list/fork operations. The deeper transcript event model
lives in the `transcript` subpackage.

## Purpose

Each san run is a session. Sessions are auto-saved as JSONL transcripts
under `~/.san/projects/<workdir-hash>/`. `--continue` and `--resume` use the
list/load APIs here to restore a previous session's messages; `--fork`
duplicates one mid-stream.

## Contract

No producer-side `Service` interface. Consumers of session don't share
a narrow common surface — the agent loop wants `NewRecorder` + `ID`;
the app composition root wants `Save` / `Load` / `LoadLatest` /
`EnsureStore` / `SetID`; the TUI selector wants `*Store` directly. A
union interface would just be `*Setup` with a rename, which TEMPLATE
Rule 3 forbids. If a future caller benefits from narrowing, declare a
small consumer-defined interface in that caller's package.

```go
package session

// Setup is the per-process session state: ID, store, recorder. Owned
// by the app composition root; accessed by feature packages through
// the methods below.
type Setup struct {
    Store     *Store
    SessionID string
    /* unexported */
}

// Identity + store
func (s *Setup) ID() string
func (s *Setup) SetID(id string)
func (s *Setup) TranscriptPath() string
func (s *Setup) GetStore() *Store
func (s *Setup) EnsureStore(cwd string) error

// Persistence (delegates to *Store)
func (s *Setup) Save(snap *Snapshot) error
func (s *Setup) Load(id string) (*Snapshot, error)
func (s *Setup) LoadLatest() (*Snapshot, error)
func (s *Setup) Fork(id string) (*Snapshot, error)

// Recorder
func (s *Setup) NewRecorder(agentID, provider, model string, maxTokens int) *Recorder
func (s *Setup) Recorder() *Recorder

// Package-level access.
func Initialize(opts Options)
func Default() *Setup
func SetDefaultSetup(s *Setup)   // test-only
func ResetDefaultSetup()         // test-only
```

### Why no interface

Consumers of session each use a different subset of `*Setup`'s
surface (agent loop wants `NewRecorder` + `ID`; app composition root
wants `Save` / `Load` / `LoadLatest` / `EnsureStore` / `SetID`; TUI
selector and subagent session bridge want `*Store` directly). With no
shared narrow surface a producer-side interface would just be
`*Setup` renamed — TEMPLATE Rule 3.

Callers that want `*Store` can read `m.services.Session.Store` (the
exported field) or call `GetStore()` (mutex-protected); both are
equivalent.

## Internals

- `Setup` (`setup.go`) — concrete implementation, holds `SessionID`,
  `Store`, and the current `Recorder` under a mutex.
- `Store` (`store.go`) — filesystem-backed JSON store under
  `~/.san/projects/<hash>/`; provides Save / Load / List / Fork.
- `Recorder` (`recorder.go`) — writes the event-sourced transcript
  (one record per inference / tool call / hook / system mutation) into
  the `transcript` subpackage's filesystem store.
- `transcript/` (subpackage) — record types, JSONL store, projector that
  reconstructs `Snapshot` from event log.
- `chat_convert.go`, `message_convert.go`, `node_convert.go` — translate
  `core.Message` between the conversation view-model, transcript content
  blocks, and transcript nodes.

## Lifecycle

- Construction: `Initialize(Options{CWD})` creates the store and a fresh
  session ID. Singleton thereafter.
- Per-run: agent emits events → `Recorder` writes records → `Snapshot`
  reconstructible at any point.
- Forks copy the underlying transcript file and assign a new session ID.

## Tests

```
internal/session/recorder_test.go         — recorder writes events correctly.
internal/session/recorder_order_test.go   — record ordering invariants.
internal/session/message_convert_test.go  — message ↔ record roundtrips.
internal/session/transcript/projector_test.go — replay correctness.
```

## See Also

- Code: `internal/session/`, `internal/session/transcript/`
- Replay UI: [`packages/inspector.md`](inspector.md)
- Layer: `feature`
