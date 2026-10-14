---
package: github.com/genai-io/san/internal/inspector
layer: feature
---

# inspector

Local web UI for replaying session transcripts. Reads the JSONL files
under `~/.san/projects/<cwd-hash>/transcripts/<session-id>.jsonl` and exposes a
small HTTP API + SSE live-tail to an embedded single-page UI.

## Purpose

When the user runs `san inspector`, this package serves a localhost-only
viewer over the project's transcripts. It is **read-only**, single-process,
and intended for debugging an agent run after the fact or watching one
live. The wire format is the JSONL on disk — the inspector adds no
schema of its own.

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
  - `GET /api/sessions/{id}/records?after={offset}` — complete JSONL records
    from a byte offset, with `X-Next-Offset` for the next read.
  - `GET /api/sessions/{id}/state/{recordID}` — materialized replay state
    at a record (system prompt + tool schemas + active message chain).
  - `GET /api/sessions/{id}/stream` — SSE live-tail of new records.
  - `GET /` — embedded SPA from `ui/assets/`.
- `replay.go` projects state; `server.go` caches 64 snapshots, keyed by record
  and file revision. Deleted or replaced files cannot return stale snapshots.
  Integrity checks belong to the selected inference, not earlier failures.
- `stream.go` polls per SSE client. Complete final records without a newline
  are emitted; incomplete JSON waits for the next poll.
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
- User guide: [`docs/inspector.md`](../../guides/inspector.md)
- Source data: [`packages/session.md`](session.md) (`internal/transcript`)
- Layer: `feature`
