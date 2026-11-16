---
package: github.com/genai-io/san/internal/flow
layer: feature
---

# flow

Parses a collaboration blueprint and schedules its subagents: dependencies,
parallel work, conditional routes, bounded workers and retries. Nodes run
through the existing subagent executor; Flow does not own another Agent,
permission or conversation lifecycle.

## Purpose

`Service` is the direct slash-command boundary: named and inline scheduling,
preflight, definitions and run recovery. It receives the native task manager and
subagent executor explicitly; it has no tool schema or registry entry. The
package owns definition format, dependency validation, scheduling, checkpoints,
run storage and final summary. Native child task/session references live in
checkpoints and attempt history, so inspection uses existing task detail.
Design and rationale:
[`design/proposal-0001-workflow-orchestration.md`](../design/proposal-0001-workflow-orchestration.md);
user-facing behaviour: [`concepts/flow.md`](../concepts/flow.md).

Natural-language authoring is explicit and app-owned: one side completion,
with a one-time guide and no tool schemas, opens an unsaved Review only on normal
completion. Token truncation, refusal or an unexpected stop reason surfaces as a
generation failure requiring explicit retry, not Review. It does not save or
launch. Ordinary main-Agent messages and tool schemas are unchanged.

The app's `/flow` management and `list|show|run|stop` commands use the same
saved-definition paths. Completion and execution share `command.ScanArguments`
quote boundaries and `command.QuoteArgument`. `Blueprint.InputNames` derives
input keys from node templates. In the TUI, missing slash-run inputs open the
same guided form as Review; supplied values are retained. The form confirms a
summary before launch and checks the saved source has not changed. Direct
service callers still get missing-input errors. Stop bypasses the foreground
input queue while its display-only result passes through the app's held-notice queue.

## Contract

```go
package flow

// The app injects the native task manager and executor into the direct service.
func NewService(tasks *task.Manager) *Service
// Launch runs a saved, reviewed definition. bounds ends with "Run ID: <id>";
// InspectRun, ResumeRun and StopRun accept that run ID or the task ID.
func (s *Service) Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
func (s *Service) InspectRun(id string) (*RunRecord, error)
func (s *Service) ResumeRun(id, from string, width int) (bounds, taskID string, err error)
func (s *Service) StopRun(id string) error

// Load lists the `*.md` definitions in dirs, earliest directory winning when
// a name appears twice, sorted by name.
func Load(dirs ...string) []Saved

// Find returns the definition named name, parsed and validated.
func Find(name string, dirs ...string) (*Blueprint, error)

// ReadDefinition returns the highest-priority definition's exact source.
func ReadDefinition(name string, dirs ...string) (string, error)

// SaveDefinition validates and atomically saves in the first directory,
// refusing to replace a source that no longer matches the editor's baseline.
func SaveDefinition(name, source, baseline string, dirs ...string) error

// Parse reads a definition and validates it: one mermaid block, one section
// per graph node and vice versa, no cycle, templates referencing only
// connected upstreams. Every problem found is reported at once.
func Parse(src string) (*Blueprint, error)

// NodeRunner executes one node with its rendered prompt. The host decides
// what a node is; an error is a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, c Call) (output string, err error)
}

// Run executes the graph: a node starts once every upstream has settled, at
// most MaxParallel run at once, and cancelling ctx stops what has not started
// while keeping what has finished.
func Run(ctx context.Context, w *Blueprint, runner NodeRunner, opts Options) *Result

func (r *Result) Failed(w *Blueprint) bool
func (r *Result) Failures(w *Blueprint) []string
func (r *Result) Summary(w *Blueprint) string

// Bounds reports the worst case a run can reach: how many subagent turns it
// can start, and the fan-out bound of each for_each node.
func (w *Blueprint) Bounds() (turns int, fanOut []string)

// InputNames lists distinct run-input keys; InputKey shares classification
// between validation, substitution and UI, including the simple {{input}} form.
func (w *Blueprint) InputNames() []string
func (w *Blueprint) InputKey(ref string) (key string, ok bool)

// References lists a node's template names in prompt order, without duplicates.
func (n *Node) References() []string

// CompactProgressView draws a short branch-and-join graph for parallel stages,
// a line for simple stages, or an exact dependency list for complex edges.
// The existing task area styles genuinely running circles with its frame clock.
func (w *Blueprint) CompactProgressView(statuses map[string]Status, width int) string
func (w *Blueprint) PreviewSteps(width int) string
```

`Blueprint`, `Node`, `Edge`, `Options`, `Result` and `NodeResult` are plain
structs; `Node.Config` carries the host-facing keys (`agent`, `mode`,
`model`) untouched.

## Internals

- `parse.go` — frontmatter (`name`, `description`, `max_parallel`) via
  yaml; a line-oriented mermaid subset (`flowchart` header, bare ids,
  `-->`, `&`, `|LABEL|`; anything else is an error naming the subset);
  `## id` sections whose leading `key: value` lines are config and whose
  remainder is the prompt; Kahn's algorithm for the cycle check; `{{ref}}`
  references checked against the node's ancestors.
- `expand.go` — bounded back edges and the for_each plan. A labelled edge
  carrying `xN` is held out of the acyclic graph, its body resolved (the
  nodes between target and source), and unrolled into one copy per round
  wired head-to-tail, each round keeping its own escape edges; an edge that
  leaves one loop enters whatever follows at its first round. `xN` is capped
  at 10 in `validate`, before unrolling, to bound parsing work during Review,
  preview and launch validation. Validation runs on the graph as drawn, so
  `{{review}}` inside `draft` resolves; the cycle check and the unroller
  work on the forward edges alone. The for_each
  plan: the first JSON value that decodes in the
  upstream's output (a model writes prose around it), navigated to the named
  field, flattened into `{{item}}` / `{{item.key}}`. A plan larger than
  `max_workers` fails the node rather than losing its tail — which is the
  only place that bound is enforced, so the fan-out needs no gate of its own
  and queues for the run's shared slots like any other node. `Bounds` counts
  the worst case for execution-limit displays and service previews/launch summaries.
- `run.go` — one goroutine per node waiting on its upstreams' done
  channels, a semaphore of `MaxParallel` held only while a turn executes
  (never while waiting, so a for_each node cannot deadlock against its own
  workers). A node's scope is keyed by `Base`, not `ID`, which is what makes
  `{{draft}}` inside `review#2` mean round 2's draft. A copy of the node
  carrying the back edge (`Node.tail`) fails when its answer takes none of
  its labelled ways out (`unanswered`), read off that round's own edges — on
  the last round the retry edge is gone, so `FAIL` itself fails, which is
  exhaustion. Every other node is exempt, a gate in the loop body included:
  a gate's unmatched answer is a successful stop. A
  node's template scope is the outputs of every ancestor reached through a
  *taken* edge, inherited downstream; an untaken conditional edge hides that
  branch.
  `Options.Resume` and `OnCheckpoint` extend this same scheduler. Worker reuse
  is keyed by position in a hashed ordered plan. `Invalidate` resets a node and
  its descendants; `Retry` retains partial workers only at retry roots. The
  execution path and recovery preview share these operations.
- `store.go` — saved-definition listing, lookup, exact-source reading and
  optimistic saves, alongside run record schema, private atomic snapshots and
  exclusive executor ownership. Listing reads only frontmatter; `Find` does the
  full parse. Search paths come from the caller. `store_unix.go` /
  `store_windows.go` contain only platform-specific owner checks. An empty run
  directory uses memory for embedded callers and tests.
- `service.go` — typed service configuration, executor preflight, saved/inline
  launch, definitions and preview. `agent.go` binds record lifecycle and graph
  progress to the native task/subagent infrastructure; no tool adapter package
  or whole-Flow tool-approval wrapper remains.
- `internal/app/flow.go` — explicit one-shot blueprint authoring, cancellation,
  session/view fences and `PurposeFlow` recording. It reuses `sideRequest` without
  advertising tools or changing the main conversation chain.
- `internal/app/input/on_flow.go` — explicit commands, authoring, save/input
  interaction and graph-level inspection/recovery. `on_flow_view.go` renders
  those pages. Review keeps the graph above selected-step data flow; launching
  returns to the composer. Node executions open the existing `TaskViewer`, not
  a second output/activity viewer. Session and view generations fence navigation.
- Node phases: `pending → running → succeeded | failed | skipped | omitted`.
  A node becomes `running` only after it acquires a shared execution slot, so
  work waiting at `max_parallel` remains pending in the live graph.
  `skipped` is contagion from an upstream failure; `omitted` means no
  upstream edge was taken. `continue_on_error` on the failing node stops the
  contagion. Downstream gets completed workers from a failed `for_each`, but
  no unfinished output from an ordinary turn.
- `view.go` owns the final summary and compact branch-and-join live views.
  The summary contains status lines and successful sink outputs; dense graphs
  fall back to an exact dependency list.
- Native subagents keep their tools, permissions, session history and output.
  Managed children register through `CreateChildAgentTask`, so normal lifecycle
  observers still run. The app projects their recent activity and completed
  answers into native Agent blocks, without model turns or fabricated tool calls.
  A visible graph replaces duplicate child tracker rows in the footer.
  `ParentTaskID` keeps recovery scheduler-owned; ordinary foreground Agent
  presentation is unchanged.

`tests/integration/flow/` covers the node ↔ subagent-turn seam the
unit tests stub out; graph semantics stay with the unit tests.

## Lifecycle

`Parse` is pure. `Run` blocks until every node has settled and is safe to
call concurrently on distinct workflows; the host owns the context, and
cancelling it fails nodes that have not started (or that observe it) while
finished results are kept. `Run` itself retains no state; `Service` persists
checkpoints through its callback. Run snapshots fix definition and inputs,
not external workspace state. Retries may repeat side effects; locks prevent
concurrent ownership, not exactly-once external actions.

- Ownership: each run holds an operating-system file lock
  (`atomicfile.TryLock`, `flock`/`LockFileEx`) on `<run-id>.lock` for as long as
  it executes. The OS drops it when the holder exits, so a crashed process never
  blocks recovery on any platform; a record still `running` with no holder is
  reported `interrupted`. Lock files are kept, not removed, while a run exists.
- Persistence: a record holds the definition snapshot, inputs, checkpoints and
  the final summary. Live tool activity is not persisted; it goes to the
  scheduler task's progress tail, and native child tasks keep the transcripts.
  Only checkpoint and final-status writes can fail a run.
- History: `List` skips (and logs) unreadable records. Starting a run prunes
  finished runs beyond the newest 50; running or locked runs are never pruned.
- Identity: the scheduler task is created with `CreateSchedulerTask`, which sets
  `TaskInfo.Scheduler`. The app identifies Flow tasks by that flag, never by the
  `flow` display name, which a user agent may share.
- Definitions are saved `0644`, as shared project files. Saves hold an
  in-process mutex and a cross-process `atomicfile.WithLock` on
  `workflow-runs/.definitions.lock`, so the on-disk conflict check cannot race
  another San instance.

## Tests

```
internal/flow/parse_test.go  — the release-check example, ancestor references, every rejection, all problems in one error.
internal/flow/run_test.go    — order and data flow, max_parallel, conditional omit (and that it never runs), scope along taken paths, failure contagion and continue_on_error, cancellation, the summary's shape.
internal/flow/expand_test.go — for_each over objects and strings, max_workers as cap and as refusal, malformed plans, for_each validation, Bounds, Load/Find priority.
internal/flow/loop_test.go   — unrolled shape, previous-round binding, early escape, exhaustion, an off-script round, gates outside and inside a loop still stopping quietly, the xN cap, loops in sequence, multi-node bodies, every loop rejection.
internal/flow/view_test.go   — compact branches, circular progress, conditional fallback and preview width.
internal/flow/resume_test.go — branch/round/worker reuse, invalidation, attempt history, cancellation and checkpoint write failure.
internal/flow/store_test.go — exact definition source, validated saves, concurrent editors, atomic run persistence, private run files, IDs, OS-lock ownership and crash release, unreadable-record skipping, pruning.
internal/flow/service_test.go — preflight, node requests, native callbacks, stream cleanup and task outcomes.
internal/flow/service_run_test.go — snapshot recovery, unpersisted activity, retry roots, stop, task-ID lookup, interruption and write failures.
internal/flow/definitions_test.go — validation, stale saves, filename collisions and project copies.
internal/app/flow_test.go — one-shot no-tools authoring, recording, cancellation and stale-result fences.
internal/session/flow_completion_test.go — Flow-purpose inference recording without ordinary conversation messages.
internal/app/input/on_flow_test.go — review/edit/input/run transitions, graph layout and recovery confirmations.
internal/app/input/on_flow_review_test.go — input/output explanations, secondary limits, source diff, retained drafts and recovery modes.
internal/app/input/slash_flow_test.go — explicit creation, Skill-derived drafts, list/show/run/stop, management, argument parsing and verb-led goals.
internal/app/command_suggestion_test.go — saved-name and input-key completion, quoted names and values, unfinished quotes.
internal/app/update_submit_test.go — immediate stop without losing streamed text or tool metadata; Ctrl+C foreground/background routing.
tests/integration/flow/flow_test.go — real subagent executor with scripted model, fan-out, truncated turns and failed-stream cleanup.
```

## See Also

- Code: `internal/flow/`
- Related packages: [`subagent`](subagent.md) (what runs a node),
  [`task`](task.md) (the background task a run lives under),
  [`tool`](tool.md) (registration and the parent-only rule)
- Concepts: [`concepts/flow.md`](../concepts/flow.md)
