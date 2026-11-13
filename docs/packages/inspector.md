---
package: github.com/genai-io/san/internal/inspector
layer: feature
---

# inspector

Read-only local web UI for native session events. TUI, ACP, print and child
execution use the same `internal/session` backend; the inspector does not own a
second event schema, projector or writer.

## Purpose and entrypoints

`san inspector` resolves the canonical project with `session.OpenStore` and passes
that store to `inspector.New(store *session.Store) *Server`.
`Server.Handler() http.Handler` serves an embedded SPA and a small HTTP/SSE API.
The command binds only to loopback; session contents may contain sensitive model
inputs and outputs, and the local API has no authentication.

Authoritative logs live under
`~/.san/projects/<project>/sessions/<sid>/events.jsonl`. There is no older-format
reader, cwd-hash partition lookup or fallback to `transcripts/`.

## Flow and API

- `GET /api/sessions`: enumerate live session directories without regenerating
  overview caches. Unsupported sessions remain discoverable for diagnosis.
  Attrs titles override generated titles; attrs errors are reported separately.
- `GET /api/sessions/{id}/records`: JSON containing unchanged committed SDK batches
  and checked tails. A read failure returns the valid prefix, an error and bounded
  explicitly unvalidated diagnostic text; diagnostics are not projected events.
- `GET /api/sessions/{id}/state/{seq}`: validated native state or the exact
  historical request at an `inference.start`. Event identity is sequence within
  the session, not a line number or byte offset. Invalid projections fail closed.
- `GET /api/sessions/{id}/stream`: SSE of committed batches. Polling compares the
  entire prior tail chain, emits only complete new batches and closes with a
  diagnostic if the prefix is replaced or corrupt. Partial suffixes wait.
- `GET /`: embedded SPA from `ui/assets/`.

`session.Store.Read`, `ContextRequest`, `InferenceRequest` and `CommittedBatches`
are the authoritative read APIs. `replay.go` is an HTTP presentation adapter, not
an independent domain replay implementation. Conversation requests reconstruct
catalogues and native context at the admitted prefix; standalone requests load
verified immutable blobs. Unsupported data remains inspectable as raw diagnostic
text, never silently normalized into supported events.

The UI exposes all thirteen event families, native content blocks, provenance,
permission authority and asked snapshots, raw tool details, child-session links,
compaction boundaries and inference accounting. Filters select event families;
request generations and stream identity fence stale browser responses. Markdown
links allow HTTP, HTTPS, mailto and relative URLs, not executable schemes.

## Lifecycle and configuration

Construction opens no session writer and launches no background work. Each SSE
request polls every 500 ms and stops when canceled. Reconnection starts from the
committed prefix; the browser deduplicates stable batch/event identities.
`http.Server.Shutdown` drains requests. Listing, replay, streaming and diagnostics
do not modify event logs, lock files, attrs or caches.

The CLI's `--addr` defaults to `127.0.0.1:0`; `--no-open` prints the URL instead of
launching a browser. Project identity follows Git common-directory/worktree rules,
not the startup directory's old encoded path.

## Tests and pitfalls

- `server_test.go`, `attrs_test.go`: routes, native state, title precedence and
  read-only behavior.
- `replay_regression_test.go`, `native_families_test.go`: exact request replay,
  unsupported/corrupt data, native message and event-family coverage.
- `stream_test.go`: complete batch visibility, replacement detection and
  diagnostics without splicing unrelated histories.
- `ui/app.test.cjs`: delayed responses/streams, filters and safe links.

Run `go test -race ./internal/inspector` and `make test-inspector-ui` for focused
checks. Do not decode raw batch payloads into a competing product projector or
acquire a writer to inspect a running session. Never present bounded unvalidated
text as accepted history or infer a tool's outcome from a missing finish.

See the [user guide](../guides/inspector.md), [session package](session.md) and
[package map](../reference/package-map.md).
