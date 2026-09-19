---
layer: infrastructure
---

# infrastructure

Stateless helpers usable from any layer above. None of these packages
own business logic.
Documented together because each surface is small and the role is the
same.

Layer: `infrastructure` (see [`../reference/dependency-rules.md`](../../reference/dependency-rules.md)).

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
- Consumer: [`packages/llm.md`](../2-feature/llm.md) for provider API keys.
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
  [`../concepts/harness-channels.md`](../../concepts/harness-channels.md).
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
- Consumers: [`skill.md`](../2-feature/skill.md), [`subagent.md`](../2-feature/subagent.md),
  `persona`, [`command.md`](../2-feature/command.md). Every skill / agent /
  persona / command file is parsed through it on `san` startup.
- Code: `internal/markdown/`.

## `internal/confdir`

One constant and one helper for the repository's configuration directory name.
It exists so packages that cannot depend on `internal/setting` still resolve
`.san` paths consistently.

```go
package confdir

const Name = ".san"

func Dir(root string) string
```

- `Dir(root)` returns `root/.san`.
- Consumers include `internal/log`, `internal/core/system`, `internal/setting`,
  and extension loaders.
- Code: `internal/confdir/`.

## `internal/proc`

Cross-platform process helpers for starting subprocesses in their own process
group/session and terminating that group where the platform supports it.

```go
package proc

func DetachSession(cmd *exec.Cmd)
func TerminateGroup(cmd *exec.Cmd, sig syscall.Signal) error
```

- Used by bash/tool, hook, and MCP transport execution paths to keep child
  process cleanup behavior centralized.
- Windows support is best-effort; callers should not assume descendant cleanup
  is complete on Windows.
- Code: `internal/proc/`.

## `internal/autoupdate`

Replaces the running `san` binary with a newer GitHub release. The running
process is never touched: the release is downloaded and extracted beside the
binary, then swapped in by rename, so the session keeps the version it started
with and the next launch runs the new one.

```go
package autoupdate

func InstallDir() string
func Managed() bool
func Latest(ctx context.Context) (string, error)
func Newer(a, b string) bool
func IsRelease(v string) bool
func Install(ctx context.Context, version string, progress func(written, total int64)) error
func Cleanup()
```

- `san update` calls it interactively, wherever the binary lives. The TUI
  calls it once at startup in the background — only when `Managed()` says the
  binary is in `InstallDir()` (`~/.local/bin`, or `%LOCALAPPDATA%\san\bin` on
  Windows), so a Homebrew, `go install`, package-manager, or dev-build binary
  is never replaced behind its owner's back — and on success shows
  `✓ vX.Y.Z installed · restart to update` in the status line.
  `SAN_DISABLE_AUTOUPDATE=1` turns the background check off; `san update`
  still works.
- `Newer` only accepts plain `X.Y.Z`, so a dev build (`git describe` suffix,
  bare hash) is never replaced and a release never downgrades; `IsRelease`
  lets the caller skip the network for one. `Install` extracts only the
  archive entry named `san`/`san.exe`, beside the binary, and fails before any
  network traffic when that directory is not writable.
- `Cleanup` runs at every launch: it removes the renamed-out old binary
  (Windows cannot delete it while it is still running) and download
  directories abandoned by a session that quit mid-install.
- Code: `internal/autoupdate/`.

## See Also

- Layer: [`../reference/dependency-rules.md`](../../reference/dependency-rules.md)
- Package map: [`../reference/package-map.md`](../../reference/package-map.md)
