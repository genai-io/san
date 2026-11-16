package flow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

// Execution associates a scheduled call with its native child task and session.
type Execution struct {
	MessageID string `json:"message_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// Call is one subagent turn, with its node, rendered prompt and optional worker.
type Call struct {
	OnExecution func(Execution)
	Node        *Node
	Prompt      string
	// Label names one worker of a for_each node; empty for an ordinary node.
	// The host uses it to distinguish the transcripts.
	Label string
}

// NodeRunner executes one call. The host decides what a node is; an error is
// a failed node.
type NodeRunner interface {
	RunNode(ctx context.Context, c Call) (output string, err error)
}

// Outcome is one execution of a node or worker: what ran, how it ended and
// what it returned.
type Outcome struct {
	Instructions string             `json:"instructions,omitempty"`
	Route        string             `json:"route,omitempty"`
	Execution    Execution          `json:"execution"`
	Status       Status             `json:"status"`
	Output       string             `json:"output,omitempty"`
	Error        string             `json:"error,omitempty"`
	StartedAt    time.Time          `json:"started_at,omitempty"`
	FinishedAt   time.Time          `json:"finished_at,omitempty"`
	Duration     time.Duration      `json:"duration,omitempty"`
	PlanHash     string             `json:"plan_hash,omitempty"`
	Workers      []WorkerCheckpoint `json:"workers,omitempty"`
}

// Checkpoint is the committed state of a node. Only successful results are
// reusable; skipped and omitted nodes are recalculated from their upstreams.
type Checkpoint struct {
	Outcome
	Attempt int       `json:"attempt,omitempty"`
	History []Attempt `json:"history,omitempty"`
}

// Attempt retains the outcome of an earlier execution without treating it as
// the current result.
type Attempt struct {
	Outcome
	Number int `json:"number"`
}

// Worker identity is its position in a fixed plan, never its display label.
type WorkerCheckpoint struct {
	Index int        `json:"index"`
	Label string     `json:"label"`
	State Checkpoint `json:"state"`
}

// Options tune one run.
type Options struct {
	Route func(*Node, string) (string, error)
	// Inputs back {{input.key}} references and {{input}} under the key "input".
	Inputs map[string]string
	// OnStatus observes every phase change, on the node's goroutine.
	OnStatus     func(node *Node, status Status)
	Resume       map[string]Checkpoint
	OnCheckpoint func(node *Node, state Checkpoint) error
}

// NodeResult is one node's outcome.
type NodeResult struct {
	Route    string
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
func (r *Result) Failures(b *Blueprint) []string {
	var out []string
	for _, n := range b.Nodes {
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
func (r *Result) Failed(b *Blueprint) bool { return len(r.Failures(b)) > 0 }

// Run executes the graph: a node starts once every upstream has settled, at
// most MaxParallel run at once, and cancelling ctx stops what has not started
// while keeping what has finished.
func Run(ctx context.Context, b *Blueprint, runner NodeRunner, opts Options) *Result {
	start := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resume := cloneStates(opts.Resume)
	type slot struct {
		done chan struct{}
		res  NodeResult
		// scope is what the node's prompt could see: the outputs of every
		// ancestor reached through a taken edge. Downstream inherits it.
		scope map[string]string
	}
	slots := make(map[string]*slot, len(b.Nodes))
	for _, n := range b.Nodes {
		slots[n.ID] = &slot{done: make(chan struct{}), res: NodeResult{Status: StatusPending}}
	}
	sem := make(chan struct{}, max(b.MaxParallel, 1))

	var wg sync.WaitGroup
	for _, n := range b.Nodes {
		wg.Go(func() {
			s := slots[n.ID]
			defer close(s.done)
			setStatus := func(st Status) {
				s.res.Status = st
				if opts.OnStatus != nil {
					opts.OnStatus(n, st)
				}
				if (st == StatusSkipped || st == StatusOmitted) && opts.OnCheckpoint != nil {
					state := cloneCheckpoint(resume[n.ID])
					state.Status = st
					if err := opts.OnCheckpoint(n, state); err != nil {
						s.res.Status, s.res.Err = StatusFailed, err
						cancel()
					}
				}
			}
			if err := ctx.Err(); err != nil {
				s.res.Err = err
				if len(n.upstream) > 0 {
					setStatus(StatusSkipped)
				} else {
					setStatus(StatusFailed)
				}
				return
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
				scope[b.byID[e.From].Base] = output
				active = true
			}
			for _, e := range n.upstream {
				up := slots[e.From]
				select {
				case <-up.done:
				case <-ctx.Done():
					s.res.Err = ctx.Err()
					setStatus(StatusSkipped)
					return
				}
				if err := ctx.Err(); err != nil {
					s.res.Err = err
					setStatus(StatusSkipped)
					return
				}
				switch up.res.Status {
				case StatusSucceeded:
					if e.Label == "" || effectiveRoute(up.res.Route, up.res.Output) == e.Label {
						take(e, up, up.res.Output)
					}
				case StatusFailed:
					from := b.byID[e.From]
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
			if err := ctx.Err(); err != nil {
				s.res.Err = err
				setStatus(StatusSkipped)
				return
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
			prior := resume[n.ID]
			if prior.Status == StatusSucceeded {
				s.res.Output, s.res.Duration, s.res.Route = prior.Output, prior.Duration, prior.Route
				setStatus(StatusSucceeded)
				return
			}
			state, err := b.executeCheckpoint(ctx, n, scope, opts, runner, sem, prior, func() { setStatus(StatusRunning) }, cancel)
			s.res.Duration = state.Duration
			s.res.Output = state.Output
			s.res.Route = state.Route
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
func effectiveRoute(route, output string) string {
	if route != "" {
		return strings.TrimSpace(route)
	}
	return strings.TrimSpace(output)
}

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

func cloneCheckpoint(c Checkpoint) Checkpoint {
	c.History = append([]Attempt(nil), c.History...)
	for i := range c.History {
		c.History[i].Workers = cloneWorkers(c.History[i].Workers)
	}
	c.Workers = cloneWorkers(c.Workers)
	return c
}

func cloneWorkers(workers []WorkerCheckpoint) []WorkerCheckpoint {
	workers = append([]WorkerCheckpoint(nil), workers...)
	for i := range workers {
		workers[i].State = cloneCheckpoint(workers[i].State)
	}
	return workers
}

func cloneStates(states map[string]Checkpoint) map[string]Checkpoint {
	out := make(map[string]Checkpoint, len(states))
	for id, c := range states {
		out[id] = cloneCheckpoint(c)
	}
	return out
}

// Invalidate returns an independent snapshot with from and its descendants
// forced to execute again. Historical outcomes remain available for inspection.
func Invalidate(b *Blueprint, states map[string]Checkpoint, from string) (map[string]Checkpoint, error) {
	if _, ok := b.Node(from); !ok {
		return nil, fmt.Errorf("unknown flow node %q", from)
	}
	out := cloneStates(states)
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		c := out[id]
		c = nextAttempt(c)
		c.Status = StatusPending
		c.PlanHash = ""
		c.Workers = nil
		out[id] = c
		for _, e := range b.byID[id].downstream {
			visit(e.To)
		}
	}
	visit(from)
	return out, nil
}

// Retry preserves partial workers only at failed or unfinished roots. Results
// below a retry root are invalidated because their inputs may change.
func Retry(b *Blueprint, original map[string]Checkpoint) (map[string]Checkpoint, error) {
	retry := func(id string) bool {
		c := original[id]
		return c.Status == StatusFailed || c.Status == StatusRunning
	}
	var hasRetryAncestor func(*Node, map[string]bool) bool
	hasRetryAncestor = func(n *Node, seen map[string]bool) bool {
		for _, edge := range n.Upstream() {
			if seen[edge.From] {
				continue
			}
			seen[edge.From] = true
			if retry(edge.From) || hasRetryAncestor(b.byID[edge.From], seen) {
				return true
			}
		}
		return false
	}
	states := cloneStates(original)
	for _, n := range b.Nodes {
		if !retry(n.ID) || hasRetryAncestor(n, map[string]bool{}) {
			continue
		}
		var err error
		states, err = Invalidate(b, states, n.ID)
		if err != nil {
			return nil, err
		}
		states[n.ID] = cloneCheckpoint(original[n.ID])
	}
	return states, nil
}

func nextAttempt(c Checkpoint) Checkpoint {
	c = cloneCheckpoint(c)
	if c.Status != "" && c.Status != StatusPending && c.Status != StatusSkipped && c.Status != StatusOmitted {
		prior := c.Outcome
		prior.Workers = cloneWorkers(c.Workers)
		c.History = append(c.History, Attempt{Outcome: prior, Number: max(c.Attempt, 1)})
		c.Attempt = max(c.Attempt, 1) + 1
	}
	if c.Attempt == 0 {
		c.Attempt = 1
	}
	c.Outcome = Outcome{Status: StatusPending, PlanHash: c.PlanHash, Workers: c.Workers}
	return c
}

func (b *Blueprint) executeCheckpoint(ctx context.Context, n *Node, scope map[string]string, opts Options, runner NodeRunner, sem chan struct{}, prior Checkpoint, running func(), abort context.CancelFunc) (Checkpoint, error) {
	state := nextAttempt(prior)
	var mu sync.Mutex
	var started sync.Once
	var writeErr error
	// All mutations and snapshots are protected, including nested worker state.
	persist := func() error {
		if writeErr != nil {
			return writeErr
		}
		if opts.OnCheckpoint != nil {
			if err := opts.OnCheckpoint(n, cloneCheckpoint(state)); err != nil {
				writeErr = fmt.Errorf("checkpoint %s: %w", n.ID, err)
				abort()
				return writeErr
			}
		}
		return nil
	}
	call := func(c Call, worker *Checkpoint) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		defer func() { <-sem }()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mu.Lock()
		started.Do(func() { state.StartedAt = time.Now(); state.Status = StatusRunning; running() })
		if worker != nil {
			worker.StartedAt = time.Now()
			worker.Status = StatusRunning
		}
		err := persist()
		mu.Unlock()
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		c.OnExecution = func(execution Execution) {
			mu.Lock()
			defer mu.Unlock()
			target := &state
			if worker != nil {
				target = worker
			}
			target.Execution = execution
			_ = persist()
		}
		out, err := runner.RunNode(ctx, c)
		if err == nil {
			err = ctx.Err()
		}
		return out, err
	}
	var out string
	var err error
	if n.ForEach == "" {
		out, err = call(Call{Node: n, Prompt: b.render(n.Prompt, scope, opts.Inputs, nil)}, nil)
	} else {
		items, planErr := plan(n, scope)
		err = planErr
		if err == nil {
			raw, _ := json.Marshal(items)
			hash := fmt.Sprintf("%x", sha256.Sum256(raw))
			state.PlanHash = hash
			state.Workers = make([]WorkerCheckpoint, len(items))
			for i, item := range items {
				wc := Checkpoint{}
				if prior.PlanHash == hash && i < len(prior.Workers) && prior.Workers[i].Index == i {
					wc = cloneCheckpoint(prior.Workers[i].State)
				}
				if wc.Status != StatusSucceeded {
					wc = nextAttempt(wc)
				}
				state.Workers[i] = WorkerCheckpoint{Index: i, Label: item[itemName], State: wc}
			}
			var wg sync.WaitGroup
			errs := make([]error, len(items))
			for i, item := range items {
				if state.Workers[i].State.Status == StatusSucceeded {
					continue
				}
				wg.Go(func() {
					wc := &state.Workers[i].State
					output, e := call(Call{Node: n, Prompt: b.render(n.Prompt, scope, opts.Inputs, item), Label: item[itemName]}, wc)
					mu.Lock()
					defer mu.Unlock()
					wc.Output = output
					wc.FinishedAt = time.Now()
					if !wc.StartedAt.IsZero() {
						wc.Duration = wc.FinishedAt.Sub(wc.StartedAt)
					}
					wc.Status = StatusSucceeded
					if e != nil {
						wc.Status = StatusFailed
						wc.Error = e.Error()
					}
					errs[i] = errors.Join(e, persist())
				})
			}
			wg.Wait()
			var sb strings.Builder
			for _, wc := range state.Workers {
				if wc.State.Status == StatusSucceeded {
					fmt.Fprintf(&sb, "## %s\n%s\n\n", wc.Label, strings.TrimSpace(wc.State.Output))
				}
			}
			out = strings.TrimSpace(sb.String())
			err = errors.Join(errs...)
		}
	}
	if err == nil && opts.Route != nil {
		state.Route, err = opts.Route(n, out)
	}
	if err == nil {
		err = unanswered(n, effectiveRoute(state.Route, out))
	}
	state.Output = out
	state.FinishedAt = time.Now()
	if !state.StartedAt.IsZero() {
		state.Duration = state.FinishedAt.Sub(state.StartedAt)
	}
	state.Status = StatusSucceeded
	if err != nil {
		state.Status = StatusFailed
		state.Error = err.Error()
	}
	if e := persist(); e != nil {
		err = errors.Join(err, e)
		state.Status = StatusFailed
		state.Error = err.Error()
	}
	return state, err
}
