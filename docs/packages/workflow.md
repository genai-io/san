---
package: github.com/genai-io/san/internal/workflow
layer: feature
---

# workflow

Parses and runs a workflow: a declarative acyclic graph whose nodes are one
subagent turn each, written as markdown with a mermaid flowchart for the
topology.

## Purpose

The `Workflow` tool (`internal/tool/workflow`) hands this package a markdown
document and gets back a validated graph, then runs it under one background
task. The package owns the format (frontmatter, one mermaid block, one
`## id` section per node), validation, scheduling, checkpoints, run storage
and the summary the parent conversation receives. Node execution enters
through `NodeRunner`; storage reuses the infrastructure `atomicfile` helper.
Design and rationale:
[`design/proposal-0001-workflow-orchestration.md`](../design/proposal-0001-workflow-orchestration.md);
user-facing behaviour: [`concepts/workflow.md`](../concepts/workflow.md).

The tool description explains Agent as an alternative for already-delegated
one-off work, without directing ordinary tasks to subagents.

The app's `/workflow` management and `list|show|run|stop` commands use the same
saved-definition paths. Completion and execution share `command.ScanArguments`
quote boundaries and `command.QuoteArgument`. `Workflow.InputNames` derives
input keys from node templates; direct slash runs report missing keys before
launch. Stop bypasses the foreground input queue while its display-only result
passes through the app's held-notice queue.

## Contract

```go
package workflow

// Load lists the `*.md` definitions in dirs, earliest directory winning when
// a name appears twice, sorted by name.
func Load(dirs ...string) []Saved

// Find returns the definition named name, parsed and validated.
func Find(name string, dirs ...string) (*Workflow, error)

// ReadDefinition returns the highest-priority definition's exact source.
func ReadDefinition(name string, dirs ...string) (string, error)

// SaveDefinition validates and atomically saves in the first directory,
// refusing to replace a source that no longer matches the editor's baseline.
func SaveDefinition(name, source, baseline string, dirs ...string) error

// Parse reads a definition and validates it: one mermaid block, one section
// per graph node and vice versa, no cycle, templates referencing only
// connected upstreams. Every problem found is reported at once.
func Parse(src string) (*Workflow, error)

// NodeRunner executes one node with its rendered prompt. The host decides
// what a node is; an error is a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, c Call) (output string, err error)
}

// Run executes the graph: a node starts once every upstream has settled, at
// most MaxParallel run at once, and cancelling ctx stops what has not started
// while keeping what has finished.
func Run(ctx context.Context, w *Workflow, runner NodeRunner, opts Options) *Result

func (r *Result) Failed(w *Workflow) bool
func (r *Result) Failures(w *Workflow) []string
func (r *Result) Summary(w *Workflow) string

// Bounds reports the worst case a run can reach: how many subagent turns it
// can start, and the fan-out bound of each for_each node.
func (w *Workflow) Bounds() (turns int, fanOut []string)

// InputNames lists the distinct {{input.key}} references in node prompts.
func (w *Workflow) InputNames() []string

// References lists a node's template names in prompt order, without duplicates.
func (n *Node) References() []string

// CompactProgressView draws a short branch-and-join graph for parallel stages,
// a line for simple stages, or an exact dependency list for complex edges.
// ActivityStreamView groups and wraps the latest node and worker events, including
// bounded tool-result summaries. The app asks an
// AgentTask for a width-aware rendering on each frame and pulses running marks.
func (w *Workflow) CompactProgressView(statuses map[string]Status, width int) string
func (w *Workflow) ActivityStreamView(events []ActivityEvent, width int) string
func (w *Workflow) PreviewSteps(width int) string
```

`Workflow`, `Node`, `Edge`, `Options`, `Result` and `NodeResult` are plain
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
  at 10 in `validate`, before unrolling, because parsing runs while the
  approval dialog is built. Validation runs on the graph as drawn, so
  `{{review}}` inside `draft` resolves; the cycle check and the unroller
  work on the forward edges alone. The for_each
  plan: the first JSON value that decodes in the
  upstream's output (a model writes prose around it), navigated to the named
  field, flattened into `{{item}}` / `{{item.key}}`. A plan larger than
  `max_workers` fails the node rather than losing its tail — which is the
  only place that bound is enforced, so the fan-out needs no gate of its own
  and queues for the run's shared slots like any other node. `Bounds` counts
  the worst case for the approval dialog.
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
- `internal/tool/workflow/workflow.go` — schema, validation and public entrypoints;
  definition I/O delegates to core storage. `run.go` owns background task
  lifecycle, live callbacks and the subagent adapter.
- `internal/app/input/on_workflow.go` — command entrypoints, draft templates,
  navigation, editing, confirmation and polling. `on_workflow_view.go` owns all
  page, graph and preview rendering. Review explains run inputs and selected-step
  data flow using `Node.References`; execution limits are a secondary page.
  Editing and inspection reuse textarea and viewport; management needs no model
  turn.
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
- Streamed text stays provisional until that inference succeeds. A failed
  inference removes its buffered and displayed text before retrying, while
  accepted text and other nodes' activity remain. Animation styles only the
  leading call-status marker, preserving command and result content.

`tests/integration/workflow/` covers the node ↔ subagent-turn seam the
unit tests stub out; graph semantics stay with the unit tests.

## Lifecycle

`Parse` is pure. `Run` blocks until every node has settled and is safe to
call concurrently on distinct workflows; the host owns the context, and
cancelling it fails nodes that have not started (or that observe it) while
finished results are kept. `Run` itself retains no state; the tool persists
checkpoints through its callback. Run snapshots fix definition and inputs,
not external workspace state. Retries may repeat side effects; locks prevent
concurrent ownership, not exactly-once external actions.

## Tests

```
internal/workflow/parse_test.go  — the release-check example, ancestor references, every rejection, all problems in one error.
internal/workflow/run_test.go    — order and data flow, max_parallel, conditional omit (and that it never runs), scope along taken paths, failure contagion and continue_on_error, cancellation, the summary's shape.
internal/workflow/expand_test.go — for_each over objects and strings, max_workers as cap and as refusal, malformed plans, for_each validation, Bounds, Load/Find priority.
internal/workflow/loop_test.go   — unrolled shape, previous-round binding, early escape, exhaustion, an off-script round, gates outside and inside a loop still stopping quietly, the xN cap, loops in sequence, multi-node bodies, every loop rejection.
internal/workflow/view_test.go   — compact branches, live phases, conditional fallback, activity grouping and wrapping.
internal/workflow/resume_test.go — branch/round/worker reuse, invalidation, attempt history, cancellation and checkpoint write failure.
internal/workflow/store_test.go — exact definition source, validated saves, concurrent editors, atomic run persistence, private files, IDs and executor ownership.
internal/tool/workflow/workflow_test.go — pre-flight checks, node requests, live tools/text, quoted input commands, stream cleanup and task outcomes.
internal/tool/workflow/run_test.go — snapshot recovery, live activity, retry roots, stop, interruption and write failures.
internal/tool/workflow/definitions_test.go — validation, stale saves, filename collisions and project copies.
internal/app/input/on_workflow_test.go — review/edit/input/run transitions, markers, activity, history, layout and recovery confirmations.
internal/app/input/on_workflow_review_test.go — input/output explanations, secondary limits, source diff, retained drafts and recovery modes.
internal/app/input/slash_workflow_test.go — explicit creation, Skill-derived drafts, list/show/run/stop, management and argument parsing.
internal/app/command_suggestion_test.go — saved-name and input-key completion, quoted names and values, unfinished quotes.
internal/app/update_submit_test.go — immediate stop without losing streamed text or tool metadata; Ctrl+C foreground/background routing.
tests/integration/workflow/workflow_test.go — real subagent executor with scripted model, fan-out, truncated turns and failed-stream cleanup.
```

## See Also

- Code: `internal/workflow/`, `internal/tool/workflow/`
- Related packages: [`subagent`](subagent.md) (what runs a node),
  [`task`](task.md) (the background task a run lives under),
  [`tool`](tool.md) (registration and the parent-only rule)
- Concepts: [`concepts/workflow.md`](../concepts/workflow.md)
