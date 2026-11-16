package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/tool"

	"github.com/genai-io/san/internal/workflow"
)

func TestPersistedRunSharesLiveTaskAndToolActivity(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{toolEvents: func(req tool.AgentExecRequest) {
		req.OnToolStart("read-1", "Read(example.go)")
		req.OnToolResult("read-1", "Read(example.go)", "package main", nil)
	}})
	info := runToCompletion(t, wt, map[string]any{"definition": "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n"})
	records, err := wt.Runs()
	if err != nil || len(records) != 1 {
		t.Fatalf("runs: %v, %v", records, err)
	}
	r := records[0]
	if r.TaskID != info.ID || r.Status != workflow.RunSucceeded || r.Nodes["a"].Status != workflow.StatusSucceeded {
		t.Fatalf("task and persisted checkpoint diverged: %+v", r)
	}
	if !strings.Contains(info.LiveView, "Read(example.go)") || !strings.Contains(info.LiveView, "1/1 finished") {
		t.Fatalf("persisted run lost upstream live view: %s", info.LiveView)
	}
	activity := strings.Join(r.Activity["a"], "\n")
	if !strings.Contains(activity, "Read(example.go)") || !strings.Contains(activity, "✓ Read(example.go) · package main") {
		t.Fatalf("tool activity not persisted: %s", activity)
	}
}

func TestResumeRetainsSavedFilenameName(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetExecutor(&scriptedExecutor{})
	definition := "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n"
	if err := wt.runStore().Put(workflow.RunRecord{ID: "named", Name: "filename-name", Definition: definition}); err != nil {
		t.Fatal(err)
	}
	bounds, _, err := wt.ResumeRun("named", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bounds, "filename-name") {
		t.Fatalf("lost name: %s", bounds)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := wt.InspectRun("named")
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == workflow.RunSucceeded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("resume timeout")
}

type activityExecutor struct {
	scriptedExecutor
	ready chan struct{}
}

func (e *activityExecutor) Run(ctx context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	for i := 0; i < 120; i++ {
		req.OnActivity(fmt.Sprintf("actual %d", i))
	}
	close(e.ready)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestRunActualActivityAndStop(t *testing.T) {
	wt := NewWorkflowTool()
	e := &activityExecutor{ready: make(chan struct{})}
	wt.SetExecutor(e)
	w, err := wt.prepare(map[string]any{"definition": "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wt.start(w, nil, "activity", 120, "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	records, err := wt.Runs()
	if err != nil || len(records) != 1 {
		t.Fatalf("runs %v %v", records, err)
	}
	r := records[0]
	if len(r.Activity["a"]) != 100 || r.Activity["a"][0] != "actual 20" {
		t.Fatalf("activity %v", r.Activity)
	}
	if _, _, err := wt.ResumeRun(r.ID, ""); err == nil {
		t.Fatal("duplicate resume accepted")
	}
	if err := wt.StopRun(r.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := wt.InspectRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == workflow.RunStopped {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stop timeout")
}

func TestRunInterruptionRequiresAbsentOwner(t *testing.T) {
	wt := NewWorkflowTool()
	wt.SetSearchPaths([]string{filepath.Join(t.TempDir(), ".san", "workflows")})
	s := wt.runStore()
	if err := s.Put(workflow.RunRecord{ID: "run", Status: workflow.RunRunning}); err != nil {
		t.Fatal(err)
	}
	release, err := s.Acquire("run")
	if err != nil {
		t.Fatal(err)
	}
	r, err := wt.InspectRun("run")
	if err != nil || r.Status != workflow.RunRunning {
		t.Fatalf("live %+v %v", r, err)
	}
	release()
	r, err = wt.InspectRun("run")
	if err != nil || r.Status != workflow.RunInterrupted {
		t.Fatalf("dead %+v %v", r, err)
	}
}
func TestResumeSnapshotReusesSuccessfulBranch(t *testing.T) {
	wt := NewWorkflowTool()
	paths := []string{filepath.Join(t.TempDir(), ".san", "workflows")}
	wt.SetSearchPaths(paths)
	if err := os.MkdirAll(paths[0], 0700); err != nil {
		t.Fatal(err)
	}
	// A newer saved file must never replace the run's original definition.
	if err := os.WriteFile(filepath.Join(paths[0], "review.md"), []byte("new and invalid definition"), 0600); err != nil {
		t.Fatal(err)
	}
	exec := &scriptedExecutor{outputs: map[string]string{"sec": "S", "report": "R"}}
	wt.SetExecutor(exec)
	r := workflow.RunRecord{ID: "retry", Definition: definition, Inputs: map[string]string{"base": "original"}, Status: workflow.RunFailed, Nodes: map[string]workflow.Checkpoint{"diff": {Status: workflow.StatusSucceeded, Output: "D", Attempt: 1}, "sec": {Status: workflow.StatusFailed, Error: "oops", Attempt: 1}, "perf": {Status: workflow.StatusSucceeded, Output: "P", Attempt: 1}}}
	if err := wt.runStore().Put(r); err != nil {
		t.Fatal(err)
	}
	wt = NewWorkflowTool()
	wt.SetSearchPaths(paths)
	wt.SetExecutor(exec)
	_, _, err := wt.ResumeRun("retry", "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := wt.InspectRun("retry")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == workflow.RunSucceeded {
			exec.mu.Lock()
			defer exec.mu.Unlock()
			if len(exec.reqs) != 2 {
				t.Fatalf("executed %d nodes, wanted failed branch + descendant", len(exec.reqs))
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("resume timeout")
}
func TestLaunchRefusesInitialStoreFailure(t *testing.T) {
	wt := NewWorkflowTool()
	exec := &scriptedExecutor{}
	wt.SetExecutor(exec)
	p := t.TempDir()
	if err := os.Mkdir(filepath.Join(p, "blocked.json"), 0700); err != nil {
		t.Fatal(err)
	}
	wt.store = workflow.NewStore(p)
	w, err := wt.prepare(map[string]any{"definition": definition})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wt.startRecord(w, workflow.RunRecord{ID: "blocked", Definition: w.Source}, "test", 120, ""); err == nil {
		t.Fatal("expected write failure")
	}
	if len(exec.reqs) != 0 {
		t.Fatal("untracked execution")
	}
}
