package flow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWorkerExecutionUsesPositionNotDuplicateLabel(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n plan --> work\n```\n\n## plan\nplan\n\n## work\nfor_each: plan.tasks\nmax_workers: 2\n\n{{item.prompt}}\n")
	if err != nil {
		t.Fatal(err)
	}
	var state Checkpoint
	var writes int
	var seq atomic.Int32
	runner := executionRunner(func(_ context.Context, c Call) (string, error) {
		if c.Node.ID == "plan" {
			return `{"tasks":[{"name":"same","prompt":"one"},{"name":"same","prompt":"two"}]}`, nil
		}
		id := Execution{TaskID: fmt.Sprintf("child-%d", seq.Add(1)), SessionID: c.Prompt}
		if c.Prompt == "one" {
			c.OnExecution(Execution{TaskID: id.TaskID}) // launch, before session assignment
			c.OnExecution(id)
		}
		c.OnExecution(id) // completion can repeat, refine, or first report an identity
		return c.Prompt, nil
	})
	res := Run(context.Background(), w, runner, Options{OnCheckpoint: func(n *Node, c Checkpoint) error {
		if n.ID == "work" {
			writes++
			state = c
		}
		return nil
	}})
	if res.Failed(w) {
		t.Fatal(res.Failures(w))
	}
	// Two worker starts, three distinct identity updates, two finishes, and the node finish.
	if writes != 8 || state.Output != "## same\none\n\n## same\ntwo" {
		t.Fatalf("writes=%d output=%q", writes, state.Output)
	}
	if len(state.Workers) != 2 || state.Workers[0].State.Execution.SessionID != "one" || state.Workers[1].State.Execution.SessionID != "two" || state.Workers[0].State.Execution.TaskID == state.Workers[1].State.Execution.TaskID {
		t.Fatalf("worker identities: %+v", state.Workers)
	}
}

func TestFanOutHistoryRetainsWorkerExecutions(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			w := mustParse(t, "```mermaid\nflowchart LR\n plan --> work\n```\n## plan\nPlan\n## work\nfor_each: plan.tasks\nmax_workers: 2\n\n{{item.prompt}}\n")
			var seq atomic.Int32
			fail := true
			runner := executionRunner(func(_ context.Context, c Call) (string, error) {
				if c.Node.ID == "plan" {
					return `{"tasks":[{"prompt":"one"},{"prompt":"two"}]}`, nil
				}
				id := fmt.Sprintf("worker-%d", seq.Add(1))
				c.OnExecution(Execution{TaskID: id, SessionID: "session-" + id})
				if fail && c.Prompt == "two" {
					return "partial", errors.New("retry")
				}
				return c.Prompt, nil
			})
			states := map[string]Checkpoint{}
			var mu sync.Mutex
			opts := Options{OnCheckpoint: func(n *Node, c Checkpoint) error {
				mu.Lock()
				defer mu.Unlock()
				states[n.ID] = c
				return nil
			}}
			if !Run(t.Context(), w, runner, opts).Failed(w) {
				t.Fatal("expected one failed worker")
			}
			prior := states["work"]
			prior.Workers[0].State.History = []Attempt{{Outcome: Outcome{Execution: Execution{TaskID: "earlier", SessionID: "earlier-session"}}, Number: 1}}
			states["work"] = prior
			if explicit {
				var err error
				states, err = Invalidate(w, states, "work")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				plan := states["plan"]
				plan.Output = `{"tasks":[{"prompt":"changed"}]}`
				states["plan"] = plan
			}
			fail = false
			opts.Resume = states
			if result := Run(t.Context(), w, runner, opts); result.Failed(w) {
				t.Fatal(result.Failures(w))
			}
			current := states["work"]
			old := current.History[len(current.History)-1]
			if old.PlanHash != prior.PlanHash || len(old.Workers) != 2 || old.Workers[0].State.Execution != prior.Workers[0].State.Execution || old.Workers[1].State.Execution != prior.Workers[1].State.Execution {
				t.Fatal("rerun lost the original plan and native worker identities")
			}
			if old.Workers[0].State.History[0].Execution.TaskID != "earlier" || current.Workers[0].State.Execution == prior.Workers[0].State.Execution {
				t.Fatal("worker history was lost or new work reused an old identity")
			}
			old.Workers[0].State.History[0].Execution.TaskID = "mutated"
			if prior.Workers[0].State.History[0].Execution.TaskID != "earlier" {
				t.Fatal("historical worker snapshots alias prior state")
			}
		})
	}
}
