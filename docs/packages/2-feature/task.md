---
package: github.com/genai-io/san/internal/task
layer: feature
---

# task

Background task manager. Long-running shell commands and subagent runs
are tracked here so the user can observe progress, kill them, and read
their output asynchronously.

## Purpose

When the agent invokes `Bash` with `execution_mode: "background_job"` or
`"background_service"`, or spawns a background subagent via `Agent`, the work is
registered as a `BackgroundTask` here.
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
- `AgentTask` (`agent_task.go`, `agent_attempt.go`) — owns one logical worker
  task with a stable ID and serialized execution attempts. Its current status
  describes the latest attempt; immutable `Attempts` retain previous failures and
  results. Callers may
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
- Every managed task keeps its launching `ParentSessionID`. Agent contexts pass
  that identity into nested workers and background shells; launches outside an
  agent use the manager's selected session. Switching sessions does not transfer
  old workers' notices or lifecycle updates into the new conversation. The task
  registry and output logs remain available for inspection.
- Terminal notifications carry the error reason and any partial output
  together. Output above the notification's inline-size limit points at a
  healthy task log, including when the task failed or was stopped; the failure
  status remains explicit. Task/attempt identity also travels as typed broker
  metadata; internal completion bookkeeping never parses the model-facing XML.
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

## Continuation and failure handling

A logical task, child conversation, and execution attempt are different identities.
Continuing an agent retains the task ID and native child-session ID; only the
execution attempt changes. A failed attempt is never rewritten as successful.

```text
Task created → attempt 1 running → attempt 1 failed
                                  ↓ main chooses a safe continuation
              same Task/session → attempt 2 running → result → parent verification
```

- While a worker runs, `SendMessage(to=<task-id>)` queues incremental instructions.
  It is enabled by default in ordinary main sessions, without group membership;
  an explicit `/tools` disable is still respected.
  Acceptance does not mean the worker has read them or completed its work.
- For new work, omit `resume_task_id` or use JSON `null`. Compatibility with
  callers that fill every optional field also treats blank strings, `new`, and
  `/dev/null` as explicit no-session placeholders. They create new work through
  the normal permission gate; they never identify an existing worker. Other
  unknown IDs, agent names, and file paths are rejected with a correction instead
  of silently starting a new conversation.
- When its execution has ended, `Agent(resume_task_id=<task-id>, prompt=...)`
  explicitly continues its native conversation. The prompt contains only the new
  instruction, not a parent-generated copy of the child's history. Continuation
  is background-only and passes through the Agent permission gate.
- A failure chain permits one corrective continuation. Two consecutive failed
  executions exhaust the recovery budget; report a blocker and choose a new plan
  rather than repeatedly waking main. Each continuation also has a finite step
  budget (the original by default, or an explicit positive override). Successful
  follow-ups reset the consecutive-failure chain.
- Native conversation events are recorded from the start of execution, separately
  from task output logs. A healthy transcript is opened under its writer lease,
  restored internally, and appended to. Missing/broken history is rejected before
  starting another attempt, avoiding a notification/recovery feedback loop.
  The lease closes before completion is
  announced, so the next attempt cannot race the previous writer.
- Main sees the failure reason, partial findings, session pointer, and attempt
  number. It can continue, take over remaining work, or report a blocker. The
  notification presents these choices without recommending continuation. It must
  inspect uncertain side effects before retrying; neither message delivery nor a
  normal worker exit proves the requested task was fulfilled.
- Running, deliberately stopped/killed, foreign-session, and missing-checkpoint
  tasks cannot be automatically resumed. A missing or damaged conversation must
  not silently become a fresh conversation. Starting genuinely new work remains
  a separate explicit Agent invocation.
- `/tasks` defaults to active workers (including queued/current approvals).
  Completed, failed, stopped, killed, and interrupted workers live in a collapsed
  History section with an explicit failure count; `h` expands/collapses it.
  Active rows come first; history is newest-launched first. Navigation only
  targets visible rows, and closing/reopening resets history to collapsed.
  This is presentation-only: failures, output logs, attempt history, and eligible
  continuation actions are not deleted or reclassified. A detail already open
  stays on the same task when it ends or starts a new attempt. Persisted ended
  tracker entries whose workers are gone remain inspectable as recorded history,
  with their stored failure reason but no fabricated live output or actions.
- `/tasks` details show the child session, output-log path when available, and
  attempt history. `c` on an eligible ended
  worker asks main to choose a safe continuation; the UI does not directly launch
  tools or bypass approval. Completion visibility is keyed by task and attempt,
  so an old notice cannot hide a newer result. At delivery, a completion from an
  older attempt of a known task is discarded before display or model wakeup;
  current failures, ordinary messages, and unscoped Bash notices still deliver.
  The viewer snapshots worker output
  on its one-second poll and on approval/completion changes, not on every stream,
  spinner, or scrolling event. Actions still revalidate ownership, status, and
  attempt against the backend before execution.
- Manual interruption holds background-driven turns until real user input;
  arriving notifications remain visible and queued rather than restarting main.
- Continuation launch checkpoints are currently process-local. Native history is
  durable, but reconstructing task ownership/policy after the entire San process
  exits is not implemented. This is not a general crash-recovery scheduler or an
  automatic dependency-DAG restart engine.

### Shell jobs and services

`Bash` defaults to `execution_mode: "foreground"`; omit the mode for ordinary
commands whose results are needed before continuing. Use
`execution_mode: "background_job"` only for independent finite work. Both finite
modes accept `timeout_ms` (default 120000, maximum 600000 milliseconds).
`FailureInfo` records observed deadline, exit, or signal evidence. A SIGKILL alone
is not diagnosed as a deadline or OOM; Unix signal exit codes use `128 + signal`.

Use explicit `execution_mode: "background_service"` for a provider/dev server
that must remain alive. Services reject a non-null `timeout_ms`, do not inherit a
finite job's deadline, and must be explicitly health-checked before dependent
work starts. Starting the process is not readiness confirmation. A spontaneous service exit, even exit 0,
is a failure; deliberate stop/cleanup is stopped. Application teardown stops all
managed services with graceful termination and bounded kill escalation. Merely
switching the selected conversation does not stop another session's service.
Services are not automatically restarted: main must inspect the reason, verify
safe restart and readiness, and rerun affected validation explicitly.

## Tests

```
internal/task/manager_test.go        — register / get / list / kill.
internal/task/bash_task_test.go      — output streaming and cancel.
internal/task/bash_service_test.go   — deadline/signal evidence and service stop semantics.
internal/task/agent_attempt_test.go  — same task identity, immutable attempts, stale completion fencing.
internal/tool/fs/shell_service_test.go — service lifetime and option validation.
internal/subagent/executor_resume_test.go — same native child conversation across execution attempts.
internal/app/task_recovery_test.go   — attempt-aware notices, manual-stop hold, UI continuation requests.
internal/app/input/task_viewer_history_test.go — collapsed history, visible selection, stable actions, narrow/short views.
internal/app/input/task_viewer_recorded_test.go — recorded history is always read-only.
internal/app/model_task_history_test.go — persisted reasons, live precedence, ownership and interrupted history.
internal/app/conv/tracker_history_test.go — announcement-aware folding and quiet secondary history.
internal/task/hooks_test.go          — lifecycle hook emission.
internal/task/session_test.go        — ownership across switches and nested launches.
internal/task/output_observability_test.go — log errors and display-only progress.
internal/task/output_disappearance_test.go — removed logs do not become false full reports.
```

## See Also

- Code: `internal/task/`, `internal/todo/`
- Spawning surface: [`packages/tool.md`](tool.md) (Bash/Agent tools), [`packages/subagent.md`](subagent.md)
- Layer: `feature`
