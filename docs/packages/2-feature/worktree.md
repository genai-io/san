---
package: github.com/genai-io/san/internal/worktree
layer: feature
---

# worktree

Thin wrapper over `git worktree add/remove` that produces an isolated
workspace under `<cwd>/.git/agent-worktrees/<slug>/` plus a cleanup
closure. Used by the standalone `EnterWorktree` tool and by the Agent
tool's `isolation: "worktree"` mode.

## Purpose

Subagents may need a clean git workspace separate from the main session's
working directory — e.g. an "explore" subagent running long-shot
experiments that must not pollute the parent's tree. This package gives
each such invocation its own worktree and the closure to clean it up.

## Contract

```go
package worktree

// Result contains the outcome of creating a worktree.
type Result struct {
    Path   string // absolute path to the worktree
    Branch string // branch name (empty for detached HEAD)
}

// Create creates a git worktree under baseCwd/.git/agent-worktrees/<slug>.
// If slug is empty, a random short ID is used.
// Returns the worktree path and a cleanup function.
func Create(baseCwd, slug string) (*Result, func(), error)

// Remove tears down the worktree at the given path.
func Remove(baseCwd, worktreePath string) error
```

No interface, no service, no singleton. Just two functions.

### Known Violations

None worth tracking. The package is exactly the right size: a tiny,
focused wrapper around `git worktree`. Use this as the reference shape
for what `infrastructure`-flavored packages should look like.

The package is labeled `feature` rather than `infrastructure` because it
fires hooks (`fireWorktreeCreated`) and depends on `log`, which crosses
the line. If the hook emission were lifted out to the caller, this would
be a pure infrastructure helper.

## Internals

- `worktree.go` — `Create` / `Remove` calling `git worktree add --detach`
  and `git worktree remove`. Validates slug against path traversal.
- `hooks.go` — fires `WorktreeCreate` / `WorktreeRemove` hooks via the
  `hook` singleton.

## Lifecycle

- Per-invocation only. `Create` returns a cleanup closure the caller is
  expected to defer.

## Tests

```
internal/worktree/worktree_test.go    — happy path, slug validation,
                                         cleanup ordering.
internal/worktree/hooks_test.go       — hook firing.
```

## See Also

- Code: `internal/worktree/`
- Callers: [`packages/tool.md`](tool.md) (EnterWorktree tool), [`packages/subagent.md`](subagent.md) (isolation mode)
- Layer: `feature`
