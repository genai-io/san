---
package: github.com/genai-io/san/internal/task
layer: feature
---

# task

Background task manager. Long-running shell commands and subagent runs
are tracked here so the user can observe progress, kill them, and read
their output asynchronously.

## Purpose

When the agent invokes `Bash` with `run_in_background: true` or spawns a
subagent via `Agent`, the work is registered as a `BackgroundTask` here.
The TUI's task panel reads from this registry; the `TaskOutput` and
`TaskList` tools let the agent itself observe its background work.

## Contract

Background task manager. Tracks bash and subagent tasks for the TUI panel and the TaskOutput/TaskList tools. The package exposes `*Tracker` directly — no Service interface.

```go
package task

// Tracker is the opaque handle. Type exported; fields unexported.
type Tracker struct { /* internal fields */ }

func (m *Tracker) RegisterTask(t BackgroundTask)
func (m *Tracker) CreateBashTask(cmd *exec.Cmd, command, description string, ctx context.Context, cancel context.CancelFunc) *BashTask
func (m *Tracker) Get(id string) (BackgroundTask, bool)
func (m *Tracker) List() []BackgroundTask
func (m *Tracker) ListRunning() []BackgroundTask
func (m *Tracker) Kill(id string) error
func (m *Tracker) Remove(id string)
func (m *Tracker) SetOutputDir(dir string) error

// Package-level access
func Initialize(opts Options)
func Default() *Tracker
func SetDefaultTracker(m *Tracker)  // test-only
func ResetDefaultTracker()          // test-only
```


## Internals

- `Tracker` (`manager.go`) — concrete implementation. Tracks active and
  completed tasks under a mutex.
- `BackgroundTask` (`types.go`) — interface implemented by `BashTask` and
  `AgentTask`.
- `BashTask` (`bash_task.go`) — wraps `*exec.Cmd`, streams stdout/stderr
  to disk, exposes `Tail`/`Read`.
- `AgentTask` (`agent_task.go`) — wraps a subagent invocation.
- `output_store.go` — filesystem-backed per-task output files under
  `<output-dir>/<task-id>.log`.
- `tracker/` (subpackage) — task state machine the session recorder
  serializes into transcripts; surfaced by the `TaskCreate` /
  `TaskUpdate` tools.

## Lifecycle

- Construction: `Initialize(Options{OutputDir})` at app start.
- Per-task: `CreateBashTask(...)` returns a `*BashTask` already
  registered. `Kill(id)` cancels the context; `Remove(id)` evicts the
  record after completion.
- Concurrency: registry is mutex-protected; per-task streams use their
  own buffers.

## Tests

```
internal/task/manager_test.go        — register / get / list / kill.
internal/task/bash_task_test.go      — output streaming and cancel.
internal/task/hooks_test.go          — lifecycle hook emission.
```

## See Also

- Code: `internal/task/`, `internal/task/tracker/`
- Spawning surface: [`packages/tool.md`](tool.md) (Bash/Agent tools), [`packages/subagent.md`](subagent.md)
- Layer: `feature`
