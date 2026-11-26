---
layer: infrastructure
---

# infrastructure

Stateless helpers usable from any layer above. None of these packages
own business logic.
Documented together because each surface is small and the role is the
same.

Layer: `infrastructure` (see [`../reference/dependency-rules.md`](../reference/dependency-rules.md)).

## `internal/infra/log`

Process-wide structured logger built on `go.uber.org/zap`.

```go
package log

func Init() error              // initialize from env; idempotent
func Logger() *zap.Logger      // process-wide logger; never nil
```

- `log.Init()` runs once at app startup (from `internal/app/tui/init.go`).
  Output is suppressed by default; `SAN_DEBUG=1` enables zap, appending
  to `~/.san/debug.log` (reset once it passes 50MB).
- Code: `internal/infra/log/`. No unit tests; behavior exercised end-to-end.

## `internal/infra/secret`

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
- `Set` and `Delete` re-read the file under `secrets.json.lock` and merge,
  so concurrent San processes keep each other's keys (atomic write).
- Consumer: [`packages/llm.md`](llm.md) for provider API keys.
- Code: `internal/infra/secret/`.

## `internal/infra/filecache`

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

- One `*Cache` per session, cleared on `/clear` and `/resume`.
- `Touch` is goroutine-safe (mutex).
- Consumers: `internal/tool/fs/` (touch on Read/Write/Edit),
  `internal/app/tui/chat/compact.go` (build on compaction). See also
  [`../concepts/harness-channels.md`](../concepts/harness-channels.md).
- Code: `internal/infra/filecache/`.

## `internal/infra/markdown`

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
  `persona`, [`command.md`](command.md). Every skill / agent /
  persona / command file is parsed through it on `san` startup.
- Code: `internal/infra/markdown/`.

## `internal/infra/confdir`

One constant and one helper for the repository's configuration directory name.
It exists so packages that cannot depend on `internal/setting` still resolve
`.san` paths consistently.

```go
package confdir

const Name = ".san"

func Dir(root string) string
```

- `Dir(root)` returns `root/.san`.
- Consumers include `internal/infra/log`, `internal/core/system`, `internal/setting`,
  and extension loaders.
- Code: `internal/infra/confdir/`.

## `internal/infra/proc`

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
- Code: `internal/infra/proc/`.

## `internal/infra/atomicfile`

Writes a unique temporary file beside the destination, then replaces the path
by rename. Failed writes remove the temporary file and retain the old contents.

- `Write` / `WriteJSON` apply the exact requested permissions; `ReadJSON`
  refuses malformed input before a read/modify/write can replace it.
- `WriteFile` follows symlinks, checks existing write access and preserves its
  permissions. New files honor the umask. `ResolvePath` also handles missing
  parent directories and dangling symlink targets for file permission checks.
- `WithLock(path, fn)` uses a stable separate lock file for cross-process
  read/modify/write operations. Keep that file in place: locking a replaced data
  file, or removing an active lock file, would allow writers to lock different
  inodes. Locks release when the callback exits or the process dies.
- `TryLock(path)` takes the same kind of lock without waiting, returning
  `ErrLocked` when another handle (in this or another process) holds it, and a
  release func otherwise. Use it for long-lived ownership such as a Flow run;
  the OS releases it if the holder crashes.
- Tests: `internal/infra/atomicfile/` covers concurrent replacement, process locking,
  permissions and path resolution.

## `internal/infra/proc`

Cross-platform process helpers. `ReadGitStatus(ctx, cwd)` in `internal/infra/proc/git.go`
reads local worktree metadata using NUL-delimited porcelain v2, supporting
renames, unborn/detached HEAD and linked worktrees. It never fetches, disables
optional index locks and fsmonitor hooks, and sanitizes repository labels.
The caller bounds the context; cancellation kills Git and its children, with a
100 ms pipe backstop. The adapter stores no configuration or global state.

`internal/infra/proc/git_test.go` covers parsing, discovery, worktrees, remote privacy,
missing Git and cancellation. Consumers should hide read failures, not treat
failed reads as clean or display raw remote URLs.

## See Also

- Layer: [`../reference/dependency-rules.md`](../reference/dependency-rules.md)
- Package map: [`../reference/package-map.md`](../reference/package-map.md)
