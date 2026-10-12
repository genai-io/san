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
- `AgentTask` (`agent_task.go`) — wraps a subagent invocation. Callers may
  publish an in-memory `LiveView` snapshot and an optional width-aware renderer
  for the TUI, independently of append-only progress events in the output
  file. Workflows reflow their activity and graph when the terminal resizes;
  the full event trail stays in the task log. Ordinary subagents publish their
  latest progress under the existing tracker row; recoverable errors stay there
  rather than becoming independent main-conversation notices.
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
- Terminal notifications carry the error reason and any partial output
  together. Output above the notification's inline-size limit points at a
  healthy task log, including when the task failed or was stopped; the failure
  status remains explicit.
- Log initialization, append, and close errors are retained in `OutputError`,
  independently of execution status. An incomplete log is not advertised through
  `OutputFile`; completion notices use a bounded UTF-8-safe preview instead.
  Only initialization creates the file. If a log disappears while a task runs,
  subsequent appends fail rather than recreating a partial file as a full report.
- `Progress` holds the latest ordinary worker activity for a single tracker row;
  it is display-only and does not become the final/model-facing output.
- Subagent launch metadata includes its resolved `PermissionMode`, recorded
  internally as `agent.permissions` before execution. The main Agent tool has
  no mode selector. Inheritance does not override a named definition's deny
  rules or internal read-only workflow policies.
- Failed/stopped tracker rows show the terminal error immediately after the
  status, rather than stale tool activity. `todo` persists that reason so the
  row remains diagnostic after restart. Tool progress puts the tool name and
  failure reason before the command preview; full diagnostics remain in the log.

## Tests

```
internal/task/manager_test.go        — register / get / list / kill.
internal/task/bash_task_test.go      — output streaming and cancel.
internal/task/hooks_test.go          — lifecycle hook emission.
internal/task/output_observability_test.go — log errors and display-only progress.
internal/task/output_disappearance_test.go — removed logs do not become false full reports.
```

## See Also

- Code: `internal/task/`, `internal/todo/`
- Spawning surface: [`packages/tool.md`](tool.md) (Bash/Agent tools), [`packages/subagent.md`](subagent.md)
- Layer: `feature`
