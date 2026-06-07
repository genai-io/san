---
layer: infrastructure
---

# infrastructure

Stateless helpers usable from any layer above. None of these packages
import any other `internal/*` package; none own business logic.
Documented together because each surface is small and the role is the
same.

Layer: `infrastructure` (see [`../reference/dependency-rules.md`](../reference/dependency-rules.md)).

## `internal/log`

Process-wide structured logger built on `go.uber.org/zap`, plus a
development-mode sidecar that writes per-turn LLM request/response/chunk
artifacts to `$DEV_DIR` for offline inspection.

```go
package log

func Init() error              // initialize from env; idempotent
func Logger() *zap.Logger      // process-wide logger; never nil
func TurnCount() int           // monotonic turn counter
func IncrementTurn()           // called once per turn boundary

func DevEnabled() bool
func WriteRequest(payload any) error
func WriteResponse(payload any) error
func WriteChunk(payload any) error
```

- `log.Init()` runs once at app startup (from `internal/app/init.go`).
  Output is suppressed by default; `SAN_DEBUG=1` enables zap with
  `lumberjack` rotation.
- `DEV_DIR` is read once at `Init` time; changing it later has no effect.
- Code: `internal/log/`. No unit tests; behavior exercised end-to-end.

## `internal/secret`

Filesystem-backed key/value store under `~/.san/secrets.json` for API
keys and tokens.

```go
package secret

type Store struct { /* unexported */ }

func Default() *Store
func (s *Store) Get(key string) (string, bool)
func (s *Store) Set(key, value string) error
func (s *Store) Delete(key string) error
func (s *Store) Keys() []string
```

- File permissions are 0600 on create. Plain JSON, not encrypted — the
  threat model is local multi-user isolation, not at-rest secrecy.
- Each `Set` re-serializes the whole file (atomic write).
- Consumer: [`packages/llm.md`](llm.md) for provider API keys.
- Code: `internal/secret/`.

## `internal/filecache`

LRU touch tracking + "file restore" block builder. The cache records
file paths the agent has read recently; the restore builder produces a
synthetic context-injection block when compaction or session resume
needs to re-hydrate the model's view of the working tree.

```go
package filecache

type Cache struct { /* unexported */ }

func New() *Cache
func (c *Cache) Touch(filePath string)
func (c *Cache) RecentEntries() []Entry  // newest first, max 20

func Build(c *Cache) string  // see restore.go; cap 5 files / 5,000 lines per file / 50,000 total
```

- One `*Cache` per session.
- `Touch` is goroutine-safe (mutex).
- Consumers: `internal/tool/fs/` (touch on Read/Write/Edit),
  `internal/app/conv/compact.go` (build on compaction). See also
  [`../concepts/harness-channels.md`](../concepts/harness-channels.md).
- Code: `internal/filecache/`.

## `internal/markdown`

YAML frontmatter parser. The smallest package in the repo: one function,
zero state.

```go
package markdown

// ParseFrontmatterFile reads a markdown file and returns
// (frontmatter, body). frontmatter is the raw YAML text between
// the opening and closing --- delimiters; body is everything after.
// If no frontmatter is found, frontmatter is "" and body is the
// whole file contents.
func ParseFrontmatterFile(path string) (frontmatter, body string, err error)
```

- Stateless, concurrent-safe (each call opens its own file).
- Consumers: [`skill.md`](skill.md), [`subagent.md`](subagent.md),
  [`identity.md`](identity.md), [`command.md`](command.md). Every
  skill / agent / identity / command file is parsed through it on
  `san` startup.
- Code: `internal/markdown/`.

## See Also

- Layer: [`../reference/dependency-rules.md`](../reference/dependency-rules.md)
- Package map: [`../reference/package-map.md`](../reference/package-map.md)
