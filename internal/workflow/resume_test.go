package workflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func capture(states map[string]Checkpoint) func(*Node, Checkpoint) error {
	var mu sync.Mutex
	return func(n *Node, c Checkpoint) error { mu.Lock(); defer mu.Unlock(); states[n.ID] = c; return nil }
}

func TestRetryInvalidatesFailedDescendantWorkers(t *testing.T) {
	w := mustParse(t, chain)
	worker := []WorkerCheckpoint{{Index: 0, State: Checkpoint{Status: StatusSucceeded, Output: "old"}}}
	original := map[string]Checkpoint{
		"a": {Status: StatusFailed, Workers: worker, Attempt: 1},
		"b": {Status: StatusFailed, Workers: worker, Attempt: 1},
		"c": {Status: StatusSucceeded, Output: "stale", Attempt: 1},
	}
	states, err := Retry(w, original)
	if err != nil {
		t.Fatal(err)
	}
	if len(states["a"].Workers) != 1 || len(states["b"].Workers) != 0 {
		t.Fatal("partial workers must survive only at retry roots")
	}
	if states["b"].Status != StatusPending || states["c"].Status != StatusPending || len(states["b"].History) != 1 {
		t.Fatalf("missing descendant invalidation/history: %+v", states)
	}
	if original["b"].Status != StatusFailed || len(original["b"].Workers) != 1 {
		t.Fatal("retry changed its input snapshot")
	}
}

func TestCheckpointWorkerResumeAndPlanChange(t *testing.T) {
	w := mustParse(t, orchestrator)
	states := map[string]Checkpoint{}
	first := &stubRunner{outputs: map[string]string{"plan": planJSON, "review/llm": "saved"}, fails: map[string]bool{"review/tool": true}}
	Run(context.Background(), w, first, Options{OnCheckpoint: capture(states)})
	if states["review"].Workers[0].State.Output != "saved" {
		t.Fatal("worker output not committed")
	}
	second := &stubRunner{outputs: map[string]string{"review/tool": "retried"}}
	res := Run(context.Background(), w, second, Options{Resume: states, OnCheckpoint: capture(states)})
	if res.Failed(w) || slices.Contains(second.calls, "review/llm") || slices.Contains(second.calls, "plan") {
		t.Fatalf("resume calls %v: %s", second.calls, res.Summary(w))
	}
	if states["review"].Attempt != 2 || len(states["review"].History) != 1 {
		t.Fatalf("history: %+v", states["review"])
	}
	changed := cloneStates(states)
	c := changed["plan"]
	c.Output = strings.Replace(planJSON, "error handling", "changed", 1)
	changed["plan"] = c
	c = changed["review"]
	c.Status = StatusFailed
	changed["review"] = c
	third := &stubRunner{}
	Run(context.Background(), w, third, Options{Resume: changed})
	if !slices.Contains(third.calls, "review/llm") || !slices.Contains(third.calls, "review/tool") {
		t.Fatalf("plan change reused workers: %v", third.calls)
	}
}

func TestCheckpointLoopRoundResume(t *testing.T) {
	w := mustParse(t, optimizer)
	states := map[string]Checkpoint{}
	first := &stubRunner{outputs: map[string]string{"spec": "S", "draft#1": "D1", "review#1": "FAIL", "draft#2": "D2"}, fails: map[string]bool{"review#2": true}}
	Run(context.Background(), w, first, Options{OnCheckpoint: capture(states)})
	second := &stubRunner{outputs: map[string]string{"review#2": "PASS", "ship": "done"}}
	res := Run(context.Background(), w, second, Options{Resume: states})
	if res.Failed(w) || !slices.Equal(second.calls, []string{"review#2", "ship"}) || second.prompts["ship"] != "Ship D2, reviewed as PASS" {
		t.Fatalf("round resume %v: %s", second.calls, res.Summary(w))
	}
}

func TestCheckpointPreservesIndependentSuccess(t *testing.T) {
	w := mustParse(t, "```mermaid\nflowchart LR\n a --> b\n c --> d\n```\n## a\na\n## b\nb\n## c\nc\n## d\nd\n")
	states := map[string]Checkpoint{"a": {Status: StatusFailed}, "b": {Status: StatusSkipped}, "c": {Status: StatusSucceeded, Output: "C"}, "d": {Status: StatusSucceeded, Output: "D"}}
	r := &stubRunner{}
	res := Run(context.Background(), w, r, Options{Resume: states})
	if res.Failed(w) || !slices.Equal(r.calls, []string{"a", "b"}) || res.Nodes["d"].Output != "D" {
		t.Fatalf("independent success lost: %v", r.calls)
	}
}

func TestCheckpointInvalidatedFailedUpstreamRerunsSuccessfulDescendants(t *testing.T) {
	w := mustParse(t, chain)
	states := map[string]Checkpoint{
		"a": {Status: StatusFailed, Attempt: 1},
		"b": {Status: StatusSucceeded, Output: "stale B", Attempt: 1},
		"c": {Status: StatusSucceeded, Output: "stale C", Attempt: 1},
	}
	invalidated, err := Invalidate(w, states, "a")
	if err != nil {
		t.Fatal(err)
	}
	r := &stubRunner{outputs: map[string]string{"a": "new A", "b": "new B", "c": "new C"}}
	res := Run(context.Background(), w, r, Options{Resume: invalidated})
	if res.Failed(w) || !slices.Equal(r.calls, []string{"a", "b", "c"}) {
		t.Fatalf("invalidated descendants reused: calls %v; %s", r.calls, res.Summary(w))
	}
	if r.prompts["b"] != "got new A" || res.Nodes["c"].Output != "new C" {
		t.Fatalf("stale descendant data: prompts %v; c %+v", r.prompts, res.Nodes["c"])
	}
}

func TestCheckpointCanceledNeverCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := mustParse(t, orchestrator)
	r := &stubRunner{}
	Run(ctx, w, r, Options{})
	if len(r.calls) != 0 {
		t.Fatalf("called on canceled context: %v", r.calls)
	}
}

func TestCheckpointWriteFailureStopsChildren(t *testing.T) {
	w := mustParse(t, chain)
	r := &stubRunner{outputs: map[string]string{"a": "complete"}}
	boom := errors.New("disk full")
	res := Run(context.Background(), w, r, Options{OnCheckpoint: func(n *Node, c Checkpoint) error {
		if c.Status == StatusSucceeded {
			return boom
		}
		return nil
	}})
	if !errors.Is(res.Nodes["a"].Err, boom) || !slices.Equal(r.calls, []string{"a"}) {
		t.Fatalf("write failure: %v, %v", res.Nodes["a"], r.calls)
	}
}

func TestCheckpointInvalidateClonesHistory(t *testing.T) {
	w := mustParse(t, chain)
	states := map[string]Checkpoint{"a": {Status: StatusSucceeded, Output: "full", Attempt: 1, Workers: []WorkerCheckpoint{{State: Checkpoint{Status: StatusSucceeded, Output: "worker"}}}}, "b": {Status: StatusFailed, Output: "partial", Attempt: 2}}
	out, err := Invalidate(w, states, "a")
	if err != nil {
		t.Fatal(err)
	}
	if out["a"].Status != StatusPending || len(out["a"].Workers) != 0 || out["a"].History[0].Output != "full" || out["b"].History[0].Output != "partial" {
		t.Fatalf("invalidated: %+v", out)
	}
	a := out["a"]
	a.History[0].Output = "mutated"
	out["a"] = a
	if states["a"].Output != "full" || states["a"].Workers[0].State.Output != "worker" {
		t.Fatal("input mutated")
	}
	if w.Source != chain {
		t.Fatal("source not exact")
	}
}

func TestCheckpointReroutesOmittedBranch(t *testing.T) {
	src := "```mermaid\nflowchart LR\n a -->|YES| b\n a -->|NO| c\n```\n## a\nroute\n## b\nyes\n## c\nno\n"
	w := mustParse(t, src)
	states := map[string]Checkpoint{"a": {Status: StatusFailed}, "b": {Status: StatusSucceeded, Output: "old"}, "c": {Status: StatusOmitted}}
	r := &stubRunner{outputs: map[string]string{"a": "NO", "c": "new"}}
	res := Run(context.Background(), w, r, Options{Resume: states})
	if res.Nodes["b"].Status != StatusOmitted || res.Nodes["c"].Output != "new" {
		t.Fatalf("bad reroute: %+v", res.Nodes)
	}
}
