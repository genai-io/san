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
`## id` section per node), every validation, the scheduler, and the summary
the parent conversation receives. It imports nothing from San — node
execution enters through `NodeRunner` — so it can move to sdk-go as
`pkg/agent/flow` once the schema settles. Design and rationale:
[`design/proposals/0001-workflow-orchestration.md`](../../design/proposals/0001-workflow-orchestration.md);
user-facing behaviour: [`concepts/workflow.md`](../../concepts/workflow.md).

## Contract

```go
package workflow

// Load lists the `*.md` definitions in dirs, earliest directory winning when
// a name appears twice, sorted by name.
func Load(dirs ...string) []Saved

// Find returns the definition named name, parsed and validated.
func Find(name string, dirs ...string) (*Workflow, error)

// Parse reads a definition and validates it: one mermaid block, one section
// per graph node and vice versa, no cycle, templates referencing only
// connected upstreams. Every problem found is reported at once.
func Parse(src string) (*Workflow, error)

// NodeRunner executes one node with its rendered prompt. The host decides
// what a node is; an error is a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, node *Node, prompt string) (output string, err error)
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
- `load.go` — directory listing, frontmatter only, so listing does not pay
  for parsing every graph; `Find` does the full parse. Search paths come
  from the caller, so the package still knows nothing about San's layout.
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
- Node phases: `pending → running → succeeded | failed | skipped | omitted`.
  `skipped` is contagion from an upstream failure; `omitted` means no
  upstream edge was taken. `continue_on_error` on the failing node stops the
  contagion, and `whole` decides what its `{{id}}` still carries: a
  `for_each` node's finished workers, nothing from a plain turn.
- `Summary` is one status line per node followed by the output of each
  succeeded sink; intermediate outputs stay in the node transcripts.

`tests/integration/workflow/` covers the node ↔ subagent-turn seam the
unit tests stub out; graph semantics stay with the unit tests.

## Lifecycle

`Parse` is pure. `Run` blocks until every node has settled and is safe to
call concurrently on distinct workflows; the host owns the context, and
cancelling it fails nodes that have not started (or that observe it) while
finished results are kept. No state outlives the call.

## Tests

```
internal/workflow/parse_test.go  — the release-check example, ancestor references, every rejection, all problems in one error.
internal/workflow/run_test.go    — order and data flow, max_parallel, conditional omit (and that it never runs), scope along taken paths, failure contagion and continue_on_error, cancellation, the summary's shape.
internal/workflow/expand_test.go — for_each over objects and strings, max_workers as cap and as refusal, malformed plans, for_each validation, Bounds, Load/Find priority.
internal/workflow/loop_test.go   — unrolled shape, previous-round binding, early escape, exhaustion, an off-script round, gates outside and inside a loop still stopping quietly, the xN cap, loops in sequence, multi-node bodies, every loop rejection.
internal/tool/workflow/workflow_test.go — the tool end to end against a scripted executor: pre-flight rejection and bounds, node requests, fan-out labels, saved workflows, a failed run becoming a failed task.
tests/integration/workflow/workflow_test.go — the same tool through the real subagent.Executor and San's own agent loop: a prompt reaching the model and its answer coming back as the node's output (sectioning, for_each), and a truncated turn failing its node.
```

## See Also

- Code: `internal/workflow/`, `internal/tool/workflow/`
- Related packages: [`subagent`](subagent.md) (what runs a node),
  [`task`](task.md) (the background task a run lives under),
  [`tool`](tool.md) (registration and the parent-only rule)
- Concepts: [`concepts/workflow.md`](../../concepts/workflow.md)
