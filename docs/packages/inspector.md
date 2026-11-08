---
package: github.com/genai-io/san/internal/inspector
layer: feature
---

# inspector

Local web UI for replaying session transcripts. Reads the JSONL files
under the resolved project's `transcripts/<session-id>.jsonl` directory and exposes a
small HTTP API + SSE live-tail to an embedded single-page UI.

## Purpose

When the user runs `san inspector`, this package serves a localhost-only
viewer over the project's transcripts. It is **read-only**, single-process,
and intended for debugging an agent run after the fact or watching one
live. The wire format is the JSONL on disk — sdk-go `pkg/session` lines, with
san's own records in `data` — and the inspector adds no schema of its own. A
record's ID is its line number. Typed logs instead use `Store.Events` through
an injected `EventReader`: stable event IDs, validated-prefix cursor pagination,
and lossless JSON payload inspection. Event detail is server-encoded as a JSON
string alongside timeline fields, so browser number parsing cannot round native
schema integers. They are explicitly inspection-only, not resumable histories.
Only SDK `ErrMode` before any accepted typed prefix selects the legacy viewer;
a legacy record after a typed prefix is corruption. Corrupt or unsupported typed
logs remain visible errors. `New(projectDir)` retains legacy viewing via the
existing read-only format probe; typed logs require an injected reader.
Typed views do not use SSE,
legacy state replay, prompt reconstruction, or legacy category filters.

`cmd/san/inspector.go` opens the actual project with `session.OpenStore` and
uses its `ProjectDir`, so worktrees and subdirectories share project identity.
Startup can perform normal project metadata registration; read-only refers to
transcript inspection, which takes no writer leases and never repairs logs.
`NewWithEventReader` borrows this reader; callers close it after HTTP shutdown.
`Server.Close` closes the server's separate legacy reader.

## Contract

```go
package inspector

// Server hosts the transcript viewer for a single project directory.
type Server struct {
    // unexported
}

func New(projectDir string) *Server
func (s *Server) Handler() http.Handler
```

Plus internal helpers (`replay.go`, `stream.go`) that the HTTP handlers
call.

## Internals

- `Server` (`server.go`) — HTTP server. Routes:
  - `GET /api/sessions` — all transcript files, enriched with saved summaries;
    unreadable files remain available for diagnosis.
  - `GET /api/sessions/{id}/events?limit={limit}&cursor={cursor}` — checked typed
    events, `nextCursor`, and `throughSeq`; default 100, maximum 500 per page.
    Cursors pin the validated prefix; refresh explicitly starts a new snapshot.
    Page size bounds output, not replay work: each page currently replays the
    full pinned prefix rather than using an indexed seek.
    Only `409 legacy_format` permits legacy fallback. Other failures remain errors.
  - `GET /api/sessions/{id}/records?after={offset}` — complete JSONL records
    from a byte offset, with `X-Next-Offset` for the next read.
  - `GET /api/sessions/{id}/state/{recordID}` — materialized replay state
    at a record (system prompt + tool schemas + conversation).
  - `GET /api/sessions/{id}/stream` — SSE live-tail of new records.
  - `GET /` — embedded SPA from `ui/assets/`.
- `replay.go` projects state; `server.go` caches 64 snapshots, keyed by record
  and file revision. Deleted or replaced files cannot return stale snapshots.
  Integrity checks belong to the selected inference, not earlier failures.
- `stream.go` polls per SSE client. A line without its newline is still being
  written and waits for the next poll.
- `ui/embed.go` + `ui/assets/` embed the static SPA. Request generations and
  stream identity prevent old responses from changing a new selection. Markdown
  links allow HTTP, HTTPS, mailto, and relative URLs, excluding executable schemes.

## Lifecycle

- Construction: `New(projectDir)` opens a read-only handle to the
  project's transcript directory. No background goroutines until a
  request comes in.
- Per-request: replay endpoints hit the LRU; each SSE connection polls every
  500 ms and stops when the request is canceled. Reconnected streams replay
  from the start; the browser deduplicates record IDs.
- Shutdown: standard `http.Server.Shutdown` drains in-flight requests.

## Tests

```
internal/inspector/server_test.go            — HTTP routes and replay correctness.
internal/inspector/replay_regression_test.go — corruption, EOF records, cache lifetime, integrity.
internal/inspector/ui/app.test.cjs           — delayed responses/streams, link safety, filters.
```

Run `make test-inspector-ui` for the browser-state tests using Node's built-in
test runner; `make ci` includes them alongside the Go tests.

## See Also

- Code: `internal/inspector/`, `internal/inspector/ui/`
- User guide: [`guides/inspector.md`](../guides/inspector.md)
- Source data: [`packages/session.md`](session.md) (`internal/transcript`)
- Layer: `feature`
