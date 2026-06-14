---
package: github.com/genai-io/san/internal/inspector
layer: feature
---

# inspector

Local web UI for replaying session transcripts. Reads the JSONL files
under `~/.san/projects/<cwd-hash>/<session-id>.jsonl` and exposes a
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

### Known Violations

This is another clean package: small surface (`New` + `Handler`), concrete
return type, no god interface. Notes:

- **No `Service` interface.** That's a feature, not a bug — the
  inspector is a CLI subcommand's worker, not something other packages
  consume.
- **`replayCacheCapacity = 64` is a magic constant.** Move into an
  `Options{ReplayCacheCapacity int}` struct so tests can override.

## Internals

- `Server` (`server.go`) — HTTP server. Routes:
  - `GET /api/sessions` — session list (from `FileStore.Index`).
  - `GET /api/sessions/{id}` — full transcript records as JSONL.
  - `GET /api/sessions/{id}/replay/{idx}` — materialized replay state
    at record N (system prompt + tool schemas + active message chain).
  - `GET /api/sessions/{id}/stream` — SSE live-tail of new records.
  - `GET /` — embedded SPA from `ui/assets/`.
- `replay.go` (`replayLRU` + `replayState`) — LRU-cached
  point-in-time projection so timeline navigation stays cheap.
- `stream.go` (`tailer`) — multiplexes one filesystem watcher across
  many concurrent SSE connections per session.
- `ui/embed.go` + `ui/assets/` — `go:embed` the static SPA.

## Lifecycle

- Construction: `New(projectDir)` opens a read-only handle to the
  project's transcript directory. No background goroutines until a
  request comes in.
- Per-request: replay endpoints hit the LRU; SSE endpoints start a
  per-session tailer that closes when all consumers disconnect.
- Shutdown: standard `http.Server.Shutdown` drains in-flight requests.

## Tests

```
internal/inspector/server_test.go    — HTTP routes, SSE dedup across
                                        reconnections, replay correctness.
```

## See Also

- Code: `internal/inspector/`, `internal/inspector/ui/`
- User guide: [`docs/inspector.md`](../../inspector.md)
- Source data: [`packages/session.md`](session.md) (`session/transcript`)
- Layer: `feature`
