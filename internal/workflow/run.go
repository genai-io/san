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

// NodeRunner executes one node with its rendered prompt. The host decides
// what a node is; an error is a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, node *Node, prompt string) (output string, err error)
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
			take := func(e Edge, up *slot, output string) {
				maps.Copy(scope, up.scope)
				scope[e.From] = output
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
					if !w.byID[e.From].ContinueOnError {
						blocked = true
					} else if e.Label == "" {
						take(e, up, "") // tolerated failure: {{from}} renders empty
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

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				s.res.Err = ctx.Err()
				setStatus(StatusFailed)
				return
			}
			defer func() { <-sem }()

			setStatus(StatusRunning)
			t0 := time.Now()
			s.scope = scope
			out, err := runner.RunNode(ctx, n, render(n.Prompt, scope, opts.Inputs))
			s.res.Duration = time.Since(t0)
			s.res.Output = out
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
