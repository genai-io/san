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
| Executor selection | `LookupAgent` for an optional custom definition |
| Main tool schema | `AgentCatalog` for enabled predefined-agent discovery metadata |
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

// Tool-description discovery metadata
func (r *Registry) AgentCatalog() string

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
  and Editor presets are removed. Enabled user/project/plugin definitions are
  automatically discoverable through **Available predefined agents** in the
  Agent tool description. `AgentCatalog` lists only names, descriptions, and
  optional `when-to-use` hints; full charters, models, tool rules and permission
  modes stay out of the main discovery context. Disabled and persona-hidden
  definitions are omitted, and the catalog refreshes whenever schemas rebuild.
- The Agent schema defaults to direct work. Delegation weighs context isolation,
  parallel execution, or distinct capabilities against briefing, repeated reads,
  and result verification. Catalog matches describe suitability after that
  decision; they are not invocation requirements. SendMessage errors for unknown task IDs guide
  the caller to verify the ID and existing results; shell tasks stay on the shell
  tools rather than being redirected to a new agent.
- Catalog discovery reflects the loaded registry, not continuous file hot-reload.
  Definition files are scanned during initialization/project-service reload;
  their bodies are loaded lazily and cached. Adding, modifying or deleting files
  is not guaranteed to affect the next invocation without a definition reload.
- `match.go` — `ToolList` pattern matching (allow/deny semantics) shared
  with the permission gate.
- `activity_tools.go` — the gate that streams each call into the
  worker's inspection trail. It refuses nothing; it is a gate rather than a
  wrapper because that is what the loop asks once per call.

## Delegation

Direct work is the default. Delegate an independent scope when parallel execution,
specialized capabilities, or separate context justify the coordination cost and
loss of shared context. For new work, foreground execution fits a dependent next
step; background execution lets the parent continue useful independent work.
Continuation keeps its existing background execution contract.

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
  exposes task briefing, an optional name, background execution, step budget,
  and an existing task ID for continuation; neither `mode` nor `model` is a tool
  parameter. Ordinary workers inherit
  parent permissions, including YOLO. Named definitions and internal workflow
  policies retain their configured limits; `deny_tools` still wins.
- `executor_interaction.go` binds each UI request to its worker/task, spawning
  and worker tool-call IDs, current cwd and resolved permission mode.
  `SetInteractions` is supplied internally by the TUI, never by model arguments.
  Foreground and background prompts share the main UI's FIFO; replies resume only
  their original caller. Worker approvals are once/reject, not parent grants.
  Actual questions also forward in YOLO. Hard denials remain hard denials, and
  absent/closed UI fails promptly. Interactive default workers can request edits,
  so their reachable edit schemas and project instructions are retained.
  Workflow nodes also carry `WorkflowOrigin` (workflow name, run task ID, node,
  and optional fan-out label) into prompt headers and permission audit details.
  This provenance stays separate from each worker's execution/broker identity;
  a model-launched workflow retains its spawning call ID, while a slash launch
  has none.
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
- Every execution exit fires `SubagentStop` with the same invocation ID as
  `SubagentStart`. Foreground and background agents retain a Task ID for follow-up
  through `SendMessage`. See [task lifecycle and continuation](task.md#continuation-and-failure-handling).
- Inbox shutdown and message acceptance share a lock. Accepted input is processed
  within the step budget or saved in native history on failure/cancellation.
  A closing inbox rejects new input so the sender can retry after completion.
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

- The parent brief defines the task and output format. A single shared
  `prompts/subagent.txt` asks for a self-contained result reflecting meaningful
  work, checks, and findings, with unfinished, blocked, or unverified work disclosed.
  There is no separate subagent behavior template; safety rules and dynamic
  environment information use the existing harness injection.

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

- `Agent` starts a fresh conversation. Its brief should include the working
  directory, relevant paths, constraints, and validation plan. `run_in_background`
  and `max_steps` configure this initial run; use `SendMessage` for follow-up.
- Continuation restores the original policy, including tool denies, and shows it
  in the approval preview. Messaging cannot bypass disabled Agent execution or
  a disabled agent definition.
- Continuation requires a healthy native transcript and the process-local
  ownership/policy checkpoint. The transcript is recorded from the first inference
  and reopened under a writer lease. That lease closes before completion is
  announced, preventing concurrent writers. Stores that only save at the end of
  a run cannot support continuation.
- Custom definition and workflow policies still distinguish read-only inspection
  from edits and unrestricted shell execution. These are internal/configured
  constraints, not default Agent presets or model-facing selectors. An unattended
  permission denial needs a compatible tool or a blocked-work report, not
  repetition of the same command.
- Workers share the filesystem. Cancellation stops execution, not edits already
  applied; independent conversations do not isolate overlapping writes.

## Tests

```
tests/integration/agent/interaction_forwarding_test.go — real tool execution after approval/rejection.
tests/integration/agent/interaction_questions_test.go — YOLO questions, independent cancellation, unavailable UI.
tests/integration/agent/workflow_interaction_origin_test.go — concurrent workflow origins, independent approvals and cancellation.
tests/integration/agent/workflow_interaction_questions_test.go — parallel node attribution and exact answer routing.
internal/app/interactions_scrollback_test.go — canceled modals resume held history before the next worker prompt.
internal/app/interactions_renderer_test.go — real TUI keyboard approval/answer routing to workers.
internal/app/conv/interaction_test.go — original reply endpoints and transport closure.
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
tests/integration/agent/result_view_test.go — real result → adapter → writer → resumed UI.
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
