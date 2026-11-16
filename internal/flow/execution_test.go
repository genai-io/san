package flow

import (
	"context"
	"errors"
	"testing"
)

type executionRunner func(context.Context, Call) (string, error)

func (f executionRunner) RunNode(ctx context.Context, c Call) (string, error) { return f(ctx, c) }

func TestExecutionCommittedBeforeReturnAndRetainedInHistory(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n a\n```\n\n## a\nwork\n")
	if err != nil {
		t.Fatal(err)
	}
	var state Checkpoint
	for _, id := range []string{"first", "second"} {
		res := Run(context.Background(), w, executionRunner(func(ctx context.Context, c Call) (string, error) {
			c.OnExecution(Execution{TaskID: id, SessionID: "session-" + id})
			if state.Execution.TaskID != id || state.Status != StatusRunning {
				t.Fatalf("identity not committed live: %+v", state)
			}
			return "done", nil
		}), Options{Resume: map[string]Checkpoint{"a": state}, OnCheckpoint: func(_ *Node, c Checkpoint) error { state = c; return nil }})
		if res.Failed(w) {
			t.Fatal(res.Failures(w))
		}
		if id == "first" {
			state.Status = StatusFailed
		}
	}
	if state.Execution.TaskID != "second" || len(state.History) != 1 || state.History[0].Execution.TaskID != "first" {
		t.Fatalf("history: %+v", state)
	}
}

func TestExecutionCheckpointFailureCancelsCall(t *testing.T) {
	w, err := Parse("```mermaid\nflowchart LR\n a\n```\n\n## a\nwork\n")
	if err != nil {
		t.Fatal(err)
	}
	diskErr := errors.New("identity write failed")
	res := Run(context.Background(), w, executionRunner(func(ctx context.Context, c Call) (string, error) {
		c.OnExecution(Execution{TaskID: "child"})
		if ctx.Err() != context.Canceled {
			t.Fatal("identity write failure did not cancel")
		}
		return "partial", ctx.Err()
	}), Options{OnCheckpoint: func(_ *Node, c Checkpoint) error {
		if c.Execution.TaskID != "" {
			return diskErr
		}
		return nil
	}})
	if !errors.Is(res.Nodes["a"].Err, diskErr) || res.Nodes["a"].Output != "partial" {
		t.Fatalf("result: %+v", res.Nodes["a"])
	}
}
