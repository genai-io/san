---
package: github.com/genai-io/san/internal/subagent
layer: feature
---

# subagent

Registry of agent definitions (markdown-defined personas with their own
system prompt, tool subset, and permission mode) plus the **Executor**
that spawns foreground or background `core.Agent` instances from within the
main agent's tool loop.

## Purpose

Where [`packages/agent.md`](agent.md) owns the *foreground* agent session,
this package owns the subagents that session spawns via the Agent tool.
Each subagent runs its own `core.Agent` loop (event observation via
`OnEvent`; no outbox) with an isolated conversation and its own
tool/permission set, starting in the session's working directory. Each run owns
later cwd changes; they do not change the parent or sibling workers. A foreground subagent blocks the
spawning turn and returns its result as the tool result; a background
subagent runs in a goroutine under a `task.AgentTask` and registers its task
id with the [`broker`](broker.md) for the length of the run — main can
`SendMessage` it while it runs, and its completion is routed to the `main`
address when it finishes.

## Contract

The package exposes the concrete `*Registry` directly. The four
production caller sites use four different subsets of its surface, so
no producer-side role interface earns its keep — TEMPLATE Rule 3.

| Caller | Methods used |
|---|---|
| `cmd/san agent` | `Get` (CLI argument validation) |
| TUI view | `ListConfigs` (color enumeration) |
| Executor selection | `LookupAgent` for an optional custom definition; no catalog is sent to the main model |
| TUI selector adapter | full surface — `ListConfigs`, `IsEnabled`, `SetEnabled`, `DisabledAt` for the `/agent` menu |

Executor construction goes through the package-level `NewExecutor`
free function (in `executor.go`), not a method on the registry. The
free function takes `hook.Handler`, keeping subagent decoupled from
the concrete `*hook.Engine`.

```go
package subagent

// Registry is an opaque handle to the agent registry. It is exported so callers can hold and pass *Registry values; all fields
// are unexported so internal state is reached only through methods.
type Registry struct { /* internal fields */ }

// Query
func (r *Registry) ListConfigs() []*AgentConfig
func (r *Registry) Get(name string) (*AgentConfig, bool)
func (r *Registry) IsEnabled(name string) bool

// State mutation (used by the TUI selector adapter)
func (r *Registry) SetEnabled(name string, enabled bool, scope setting.Scope) error
func (r *Registry) DisabledAt(scope setting.Scope) map[string]bool

// System prompt
func (r *Registry) AgentsSection() string

// Loader bootstrapping
func (r *Registry) Register(config *AgentConfig)
func (r *Registry) InitStores(cwd string) error

// Executor construction (package-level free function)
func NewExecutor(provider llm.Provider, cwd, parentModelID string, hooks hook.Handler) *Executor

// Package-level access
func Initialize(opts Options) error
func Default() *Registry
func SetDefaultRegistry(r *Registry)  // test-only
func ResetDefaultRegistry()           // test-only
```

## Internals

- `Registry` (`registry.go`) — `AgentConfig` map keyed by name, plus
  enable state stores (user + project).
- `Executor` (`executor.go`) — spawns a `core.Agent` for one subagent
  invocation, manages its lifecycle (workspace, permission gate, hooks,
  session persistence), and returns the aggregated result.
- `executor_prompt.go` / `executor_run.go` / `executor_session.go` —
  split executor concerns (charter assembly, run loop, session attribution).
- `loader.go` — reads markdown agent definitions from `.san/agents/`
  (project, then user), `.claude/agents/` (Claude Code compatible), and
  plugin paths; lower-priority sources load first so higher ones win by
  name. Accepts alias frontmatter keys (`tools`, `allowed-tools`,
  `permission-mode`) alongside the canonical ones.
- The Agent panel's Built-in tab contains one default `Agent` entry. Explorer
  and Editor presets are removed. User/project definitions remain manageable,
  but the main tool schema does not embed a catalog of agent types.
- `match.go` — `ToolList` pattern matching (allow/deny semantics) shared
  with the permission gate.
- `activity_tools.go` — the gate that streams each call into the
  worker's inspection trail. It refuses nothing; it is a gate rather than a
  wrapper because that is what the loop asks once per call.

## Lifecycle

- Construction: `Initialize(Options{CWD, PluginAgentPaths})` loads
  definitions, initializes state stores.
- Per-invocation: `NewExecutor(provider, cwd, model, hookEngine)` →
  `Executor.Run(ctx, req)` spawns a `core.Agent`, blocks until end of
  turn, returns the aggregated `AgentResult`. `RunBackground(req)` wraps
  the same run in a `task.AgentTask` goroutine and registers with the broker under
  the task id so main can message it mid-run.
- Background permissions are resolved before task registration and goroutine
  scheduling, and recorded internally as `agent.permissions`. Later parent-mode
  changes affect new runs, not a worker already launched. The main Agent tool
  exposes only task briefing, an optional name, background execution, and step
  budget; neither `mode` nor `model` is a tool parameter. Ordinary workers inherit
  parent permissions, including YOLO. Named definitions and internal workflow
  policies retain their configured limits; `deny_tools` still wins.
- Worker models are configuration-driven: `subagentModel` in scoped
  `settings.json` selects the default worker model (ID, alias, or `vendor/model`).
  Empty/unset uses the current main-session model; `"inherit"` explicitly resets
  a lower-scope override. A named custom definition retains its own `model:`
  setting. The tool caller does not choose a model.
- Recoverable tool errors and inference retries stay in the worker's progress
  and inspection trail. Foreground calls stream them into their own activity;
  background calls append them to the task log and update the task's `LiveView`.
  Structured workflow callbacks finish the existing tool row rather than
  adding a duplicate error row. These events do not send broker notices to main:
  a failed attempt is not a failed task. Terminal results and explicit
  `SendMessage` calls are the parent communication boundary.
- Every exit path — success, cancel, error — fires `SubagentStop` with
  the same agent id `SubagentStart` carried. Subagents are one-shot: a run
  is not resumable; to continue a line of work, spawn a fresh subagent.
- The agent model is flat: only the main conversation spawns subagents. The
  `Agent` tool is parent-only in `tool.Set`, so a subagent never sees it —
  nothing to enforce at runtime.
- Concurrency: multiple executors may run in parallel; the registry is
  RWMutex-protected. Tool side effects are routed through a per-run handler:
  model tool-call IDs may repeat across workers and must not share handoff keys.
- Every `ToolEnd` drains its worker's handoff, including canceled calls. Absolute
  cwd handoffs apply only after the batch ends; all siblings use the initial
  batch directory, and the last cwd-changing call in request order wins. Only
  the worker's Environment section changes, and the next inference refreshes
  the system prompt. The parent/global cwd never changes.
- File-reading guidance is built from the effective schemas after global
  disables, per-agent allow/deny lists, and mode filtering. When `Read` is
  unavailable, workers are told how to inspect source with their offered
  shell. Explore still rejects arbitrary interpreters such as Python; the
  file-reading guidance does not widen permissions. Workers with no built-in
  reader are told to report inaccessible source as blocked work.
- A successful run means the agent ended normally, not that its stated task
  was fulfilled. Callers must read the result and retain any incomplete
  coverage or blockers; a permission-denied review is not a clean review.

## Results and display

- Delegation should return usable evidence at less total effort than the parent
  doing the same work. Treat runtime state, task fulfillment, and display as
  separate concerns: a tool failure can recover, an ended turn can still report
  blocked work, and a full diagnostic belongs in inspection rather than every
  summary. Adding retries or widening permissions does not fix missing context
  or a stale path.
- A normal end of the model turn produces `Success=true`; this records the
  execution outcome, not proof that the requested work was fulfilled. The
  parent must verify the returned findings or changes. New foreground result
  rows say `Finished`, `Failed`, or `Stopped` to describe execution.
- Foreground success and failure share one result projection. The model-facing
  text contains findings, agent identity, and a transcript pointer. Model, token
  usage, duration, and the activity/error trail travel as
  `toolresult.AgentDetails`; session records preserve them under `agent_details`.
  The UI does not parse these values out of worker prose. Legacy header-based
  transcripts still render, including `Tokens: in=N out=M`.
- Cancellation and inference failure can return both an unfinished result and
  an error. `ExecutorAdapter`, the Agent tool, Workflow, and headless `agent run` preserve the partial content,
  usage, agent session id, and transcript path. A failed foreground tool result
  includes its transcript path so the parent can inspect work already done.
  Workflow records unfinished node findings and inspection pointers in the task
  output, and counts failed workers' steps and tokens while retaining failure.
- Legacy tool adaptation keeps stdout/stderr and the failure reason in the
  model-facing content, but uses the separate error reason for error notices.
  Command output should not be duplicated into an error summary.
- Completing a foreground Agent call clears only that call's live activity;
  other agents in the same parallel tool batch keep their progress.
- Background failure and stop notifications include partial output under the
  same inline-size limit as successful output. Larger results point at the
  task output file. A failure must not hide usable findings.
- An agent notice carrying model content uses that same envelope message for
  live display and persistence; it does not additionally record `notice.added`.
  Display-only notices still use notice records. Existing notice records are
  retained; this change does not rewrite older transcripts.
- Injected notices do not interrupt tool/result pairing; actual user input does.
  Notification summaries reserve terminal status and reason before shortening
  descriptions. Failure reasons come from the first error line, not partial
  findings. Group batches persist their count and unattended-turn context.
- Tool failures are collapsed by default, with a bounded reason. `ctrl+o` or
  `/history` exposes full diagnostics and partial results; background completion
  messages can also expand in history. Display limits do not truncate stored
  content. Task descriptions and file pointers are escaped as XML attributes.
- Live foreground progress also limits each activity entry to one display row.
  Expanding it retains the full diagnostic; the stored activity is unchanged.

## Configuration and limits

- Subagents start with fresh conversations. Supply the actual working directory,
  source paths, relevant constraints, and a concrete expected result in the
  delegated prompt. Spawning another worker does not resume the previous one.
- Custom definition and workflow policies still distinguish read-only inspection
  from edits and unrestricted shell execution. These are internal/configured
  constraints, not default Agent presets or model-facing selectors. An unattended
  permission denial needs a compatible tool or a blocked-work report, not
  repetition of the same command.
- Workers share the filesystem. Cancellation stops execution, not edits already
  applied; independent conversations do not isolate overlapping writes.

## Tests

```
internal/subagent/executor_test.go      — end-to-end run scenarios.
internal/subagent/executor_cwd_test.go  — deterministic batch handoffs and cancellation.
internal/subagent/executor_cwd_isolation_test.go — repeated IDs cannot cross worker/main ownership.
tests/integration/agent/cwd_test.go     — live adapters and next-inference Environment refresh.
internal/subagent/lazy_loading_test.go  — config files read on demand.
internal/subagent/scenarios_test.go     — common invocation shapes.
tests/integration/agent/agent_test.go    — runtime, permissions, failed transcript projection.
internal/tool/agent/agent_test.go        — foreground result and error preservation.
internal/app/conv/update_test.go         — sibling activity survives agent completion.
internal/app/notify_test.go              — bounded successful and partial failure notices.
internal/app/notice_step_inject_test.go  — one live/persisted completion, delivery during inference.
internal/app/model_history_test.go       — inspectable background results without state mutation.
internal/app/conv/failure_render_test.go  — collapsed/expanded diagnostics and legacy usage.
tests/integration/agent/result_view_test.go — real result → adapter → recorder → resumed UI.
internal/tool/workflow/workflow_test.go    — unfinished node output, identity, and parallel usage.
internal/app/group_test.go                — live and persisted batch context.
tests/integration/agent/file_read_test.go — unattended errors stay in live task progress and log.
```

## See Also

- Code: `internal/subagent/`
- Message routing: [`broker`](broker.md)
- Parent agent: [`packages/agent.md`](agent.md)
- Spawning tools: [`packages/tool.md`](tool.md) (Agent, SendMessage)
- Background tasks: [`packages/task.md`](task.md)
- Layer: `feature`
