package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"
)

// Status is a node's phase. The scheduler owns it; nodes never see it.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	// StatusSkipped is failure by contagion: an upstream failed and did not
	// carry continue_on_error.
	StatusSkipped Status = "skipped"
	// StatusOmitted means no upstream edge was taken — every conditional
	// pointed elsewhere or every upstream was itself omitted.
	StatusOmitted Status = "omitted"
)

// Call is one subagent turn: a node, the prompt it was rendered into, and —
// for a node fanned out by for_each — which item it is working on.
type Call struct {
	Node   *Node
	Prompt string
	// Label names one worker of a for_each node; empty for an ordinary node.
	// The host uses it to distinguish the transcripts.
	Label string
}

// NodeRunner executes one call. The host decides what a node is; an error is
// a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, c Call) (output string, err error)
}

// Options tune one run.
type Options struct {
	// Inputs back {{input.key}} references.
	Inputs map[string]string
	// OnStatus observes every phase change, on the node's goroutine.
	OnStatus func(node *Node, status Status)
}

// NodeResult is one node's outcome.
type NodeResult struct {
	Status   Status
	Output   string
	Err      error
	Duration time.Duration
}

// Result is the outcome of a run.
type Result struct {
	Nodes    map[string]*NodeResult
	Duration time.Duration
}

// Failures lists what went wrong, as "id: reason" — empty when the run
// succeeded. A failure the node tolerated is not listed: `continue_on_error`
// says the graph may carry on without it, so it is not part of the verdict.
// Cancellation is the exception, because a node that never finished tolerates
// nothing.
func (r *Result) Failures(w *Workflow) []string {
	var out []string
	for _, n := range w.Nodes {
		switch res := r.Nodes[n.ID]; res.Status {
		case StatusSkipped:
			out = append(out, n.ID+": skipped")
		case StatusFailed:
			if !n.ContinueOnError || errors.Is(res.Err, context.Canceled) {
				out = append(out, fmt.Sprintf("%s: %v", n.ID, res.Err))
			}
		}
	}
	return out
}

// Failed reports whether the run failed, by the same rule Failures lists.
func (r *Result) Failed(w *Workflow) bool { return len(r.Failures(w)) > 0 }

// Run executes the graph: a node starts once every upstream has settled, at
// most MaxParallel run at once, and cancelling ctx stops what has not started
// while keeping what has finished.
func Run(ctx context.Context, w *Workflow, runner NodeRunner, opts Options) *Result {
	start := time.Now()
	type slot struct {
		done chan struct{}
		res  NodeResult
		// scope is what the node's prompt could see: the outputs of every
		// ancestor reached through a taken edge. Downstream inherits it.
		scope map[string]string
	}
	slots := make(map[string]*slot, len(w.Nodes))
	for _, n := range w.Nodes {
		slots[n.ID] = &slot{done: make(chan struct{}), res: NodeResult{Status: StatusPending}}
	}
	sem := make(chan struct{}, max(w.MaxParallel, 1))

	var wg sync.WaitGroup
	for _, n := range w.Nodes {
		wg.Go(func() {
			s := slots[n.ID]
			defer close(s.done)
			setStatus := func(st Status) {
				s.res.Status = st
				if opts.OnStatus != nil {
					opts.OnStatus(n, st)
				}
			}

			// Settle upstreams first. Waiting holds no semaphore slot, so a
			// deep graph cannot starve itself.
			scope := map[string]string{}
			active, blocked := false, false
			// Keyed by Base, not ID: `{{draft}}` inside review#2 means the
			// draft of round 2, and a round's chain carries only its own
			// instances, so the name resolves to the right one.
			take := func(e Edge, up *slot, output string) {
				maps.Copy(scope, up.scope)
				scope[w.byID[e.From].Base] = output
				active = true
			}
			for _, e := range n.upstream {
				up := slots[e.From]
				<-up.done
				switch up.res.Status {
				case StatusSucceeded:
					if e.Label == "" || strings.TrimSpace(up.res.Output) == e.Label {
						take(e, up, up.res.Output)
					}
				case StatusFailed:
					from := w.byID[e.From]
					switch {
					case !from.ContinueOnError:
						blocked = true
					case e.Label == "":
						// A fan-out hands on the workers that finished: whole
						// work, just less of it than the plan asked for. A
						// plain turn's output is the half-answer it stopped
						// on, which downstream would read as the finished
						// thing, so it hands on nothing.
						out := ""
						if from.ForEach != "" {
							out = up.res.Output
						}
						take(e, up, out)
					}
				case StatusSkipped:
					blocked = true
				}
			}
			switch {
			case blocked:
				setStatus(StatusSkipped)
				return
			case len(n.upstream) > 0 && !active:
				setStatus(StatusOmitted)
				return
			}

			s.scope = scope
			setStatus(StatusRunning)
			t0 := time.Now()
			out, err := execute(ctx, n, scope, opts.Inputs, runner, sem)
			s.res.Duration = time.Since(t0)
			s.res.Output = out
			if err == nil {
				err = unanswered(n, out)
			}
			if err != nil {
				s.res.Err = err
				setStatus(StatusFailed)
				return
			}
			setStatus(StatusSucceeded)
		})
	}
	wg.Wait()

	r := &Result{Nodes: make(map[string]*NodeResult, len(slots)), Duration: time.Since(start)}
	for id, s := range slots {
		res := s.res
		r.Nodes[id] = &res
	}
	return r
}

// execute runs a node: one turn, or — with for_each — one turn per plan item,
// bounded by the node's max_workers and by the run's shared slots.
func execute(ctx context.Context, n *Node, scope, inputs map[string]string, runner NodeRunner, sem chan struct{}) (string, error) {
	if n.ForEach == "" {
		return run1(ctx, runner, sem, Call{Node: n, Prompt: render(n.Prompt, scope, inputs, nil)})
	}
	items, err := plan(n, scope)
	if err != nil {
		return "", err
	}
	// max_workers is enforced in plan(), which refuses a larger plan outright,
	// so the fan-out is already at most that wide here and needs no second
	// gate; the run's shared slots still decide how many turns are in flight.
	outs := make([]string, len(items))
	errs := make([]error, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() {
			outs[i], errs[i] = run1(ctx, runner, sem, Call{
				Node:   n,
				Prompt: render(n.Prompt, scope, inputs, item),
				Label:  item[itemName],
			})
		})
	}
	wg.Wait()

	// Only the workers that finished are reported, so what a failed fan-out
	// leaves behind is whole work rather than gaps under headings.
	var b strings.Builder
	for i, item := range items {
		if errs[i] != nil {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", item[itemName], strings.TrimSpace(outs[i]))
	}
	return strings.TrimSpace(b.String()), errors.Join(errs...)
}

// unanswered fails a loop's tail when its answer takes none of its ways out.
//
// The node carrying a back edge is the one place the author declares every
// answer — the retry label, and the labels on its escapes — so an answer
// outside that set is a model gone off-script, and the round fails instead of
// silently omitting everything after it. On the last round the retry has
// nowhere left to go, so FAIL itself is outside the set: that is exhaustion.
// Every other node, a gate inside a loop body included, is exempt: a gate's
// unmatched NO is how it says "stop here", and nothing in the graph tells a
// gate from a router.
func unanswered(n *Node, out string) error {
	if !n.tail || len(n.downstream) == 0 {
		return nil
	}
	out = strings.TrimSpace(out) // as the edge-taking rule compares it
	var want []string
	for _, e := range n.downstream {
		if e.Label == "" || e.Label == out {
			return nil // an unconditional way out, or the answer's own edge
		}
		want = append(want, e.Label)
	}
	return fmt.Errorf("returned %q; its edges want %s", out, strings.Join(want, " or "))
}

// run1 takes a shared slot for the length of one turn. Waiting for a slot
// happens here and nowhere else, so a node never holds one while it waits.
func run1(ctx context.Context, runner NodeRunner, sem chan struct{}, c Call) (string, error) {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-sem }()
	return runner.RunNode(ctx, c)
}

// Summary renders the run for the conversation that launched it: one status
// line per node, then the output of every sink node that succeeded. Sinks
// are the workflow's product; intermediate outputs stay in the transcripts.
func (r *Result) Summary(w *Workflow) string {
	var b strings.Builder
	verdict := "succeeded"
	if r.Failed(w) {
		verdict = "failed"
	}
	fmt.Fprintf(&b, "workflow %s %s in %s\n", w.Name, verdict, r.Duration.Round(time.Second))
	for _, n := range w.Nodes {
		res := r.Nodes[n.ID]
		fmt.Fprintf(&b, "  %s %s", statusGlyph(res.Status), n.ID)
		if res.Duration > 0 {
			fmt.Fprintf(&b, " (%s)", res.Duration.Round(time.Second))
		}
		if res.Err != nil {
			fmt.Fprintf(&b, ": %v", res.Err)
		}
		b.WriteString("\n")
	}
	for _, n := range w.Nodes {
		res := r.Nodes[n.ID]
		if len(n.downstream) > 0 || res.Status != StatusSucceeded || strings.TrimSpace(res.Output) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n%s\n", n.ID, strings.TrimSpace(res.Output))
	}
	return b.String()
}

func statusGlyph(s Status) string {
	switch s {
	case StatusSucceeded:
		return "✓"
	case StatusFailed:
		return "✗"
	case StatusSkipped:
		return "↷"
	case StatusOmitted:
		return "⊘"
	default:
		return "○"
	}
}
