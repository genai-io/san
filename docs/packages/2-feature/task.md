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
The TUI's task panel reads from this registry; completion notifications flow
back to the agent automatically. `AgentStop` cancels a running agent task; on
Unix, a background Bash command reports its own process-group ID so Bash can
terminate it directly.

## Contract

Background task manager. Tracks bash and subagent tasks for the TUI panel and
`AgentStop`. The package exposes `*Manager` directly — no Service interface.

```go
package task

type Manager struct { /* internal fields */ }

func (m *Manager) CreateBashTask(...) *BashTask
func (m *Manager) CreateAgentTask(...) *AgentTask
func (m *Manager) RegisterTask(t BackgroundTask)
func (m *Manager) Get(id string) (BackgroundTask, bool)
func (m *Manager) List() []BackgroundTask
func (m *Manager) ListRunning() []BackgroundTask
func (m *Manager) Kill(id string) error
func (m *Manager) Remove(id string)
func (m *Manager) SetOutputDir(dir string) error

// Package-level access
func Initialize(opts Options) error
func Default() *Manager
func SetDefaultTracker(m *Manager) // test-only
func ResetDefaultTracker()         // test-only
```


## Internals

- `Manager` (`manager.go`) — tracks active and completed tasks and owns its
  output directory. Multiple managers cannot redirect one another's files.
- `BackgroundTask` (`types.go`) — interface implemented by `BashTask` and
  `AgentTask`.
- `BashTask` (`bash_task.go`) — wraps `*exec.Cmd`, streams stdout/stderr
  to disk, exposes `Tail`/`Read`.
- `AgentTask` (`agent_task.go`) — wraps a subagent invocation.
- `output_store.go` — filesystem-backed per-task output files under
  `<output-dir>/<task-id>.log`.
- Agent planning items are a separate capability in `internal/todo`; the
  session layer adapts them into transcript-owned persistence views.

## Lifecycle

- Construction: `Initialize(Options{OutputDir})` at app start.
- Per-task: `CreateBashTask(...)` and `CreateAgentTask(...)` return tasks
  already registered with that manager. `Kill(id)` cancels the context;
  `Remove(id)` evicts the record after completion.
- Concurrency: registry is mutex-protected; per-task streams use their
  own buffers.

## Tests

```
internal/task/manager_test.go        — register / get / list / kill.
internal/task/bash_task_test.go      — output streaming and cancel.
internal/task/hooks_test.go          — lifecycle hook emission.
```

## See Also

- Code: `internal/task/`, `internal/todo/`
- Spawning surface: [`packages/tool.md`](tool.md) (Bash/Agent tools), [`packages/subagent.md`](subagent.md)
- Layer: `feature`
