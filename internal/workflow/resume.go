package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

func cloneCheckpoint(c Checkpoint) Checkpoint {
	c.History = append([]Attempt(nil), c.History...)
	c.Workers = append([]WorkerCheckpoint(nil), c.Workers...)
	for i := range c.Workers {
		c.Workers[i].State = cloneCheckpoint(c.Workers[i].State)
	}
	return c
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
func Invalidate(w *Workflow, states map[string]Checkpoint, from string) (map[string]Checkpoint, error) {
	if _, ok := w.Node(from); !ok {
		return nil, fmt.Errorf("unknown workflow node %q", from)
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
		for _, e := range w.byID[id].downstream {
			visit(e.To)
		}
	}
	visit(from)
	return out, nil
}

// Retry preserves partial workers only at failed or unfinished roots. Results
// below a retry root are invalidated because their inputs may change.
func Retry(w *Workflow, original map[string]Checkpoint) (map[string]Checkpoint, error) {
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
			if retry(edge.From) || hasRetryAncestor(w.byID[edge.From], seen) {
				return true
			}
		}
		return false
	}
	states := cloneStates(original)
	for _, n := range w.Nodes {
		if !retry(n.ID) || hasRetryAncestor(n, map[string]bool{}) {
			continue
		}
		var err error
		states, err = Invalidate(w, states, n.ID)
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
		c.History = append(c.History, Attempt{Number: max(c.Attempt, 1), Status: c.Status, Output: c.Output, Error: c.Error, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, Duration: c.Duration})
		c.Attempt = max(c.Attempt, 1) + 1
	}
	if c.Attempt == 0 {
		c.Attempt = 1
	}
	c.Status = StatusPending
	c.Output = ""
	c.Error = ""
	c.StartedAt = time.Time{}
	c.FinishedAt = time.Time{}
	c.Duration = 0
	return c
}

func executeCheckpoint(ctx context.Context, n *Node, scope map[string]string, opts Options, runner NodeRunner, sem chan struct{}, prior Checkpoint, running func(), abort context.CancelFunc) (Checkpoint, error) {
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
		out, err := runner.RunNode(ctx, c)
		if err == nil {
			err = ctx.Err()
		}
		return out, err
	}
	var out string
	var err error
	if n.ForEach == "" {
		out, err = call(Call{Node: n, Prompt: render(n.Prompt, scope, opts.Inputs, nil)}, nil)
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
					output, e := call(Call{Node: n, Prompt: render(n.Prompt, scope, opts.Inputs, item), Label: item[itemName]}, wc)
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
			var b strings.Builder
			for _, wc := range state.Workers {
				if wc.State.Status == StatusSucceeded {
					fmt.Fprintf(&b, "## %s\n%s\n\n", wc.Label, strings.TrimSpace(wc.State.Output))
				}
			}
			out = strings.TrimSpace(b.String())
			err = errors.Join(errs...)
		}
	}
	if err == nil {
		err = unanswered(n, out)
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
