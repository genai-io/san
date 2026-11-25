package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/tool"

	"github.com/genai-io/san/internal/task"
)

func TestPersistedRunSharesLiveTaskWithoutCopyingActivity(t *testing.T) {
	wt := NewService(task.Default())
	wt.SetExecutor(&scriptedExecutor{outputs: map[string]string{"a": " final answer \n"}, toolEvents: func(req tool.AgentExecRequest) {
		req.OnToolStart("read-1", "Read(example.go)")
		req.OnToolResult("read-1", "Read(example.go)", "package main", nil)
	}})
	info := runToCompletion(t, wt, map[string]any{"definition": "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n"})
	records, err := wt.Runs()
	if err != nil || len(records) != 1 {
		t.Fatalf("runs: %v, %v", records, err)
	}
	r := records[0]
	if info.Output != "final answer" || !strings.Contains(r.Output, "succeeded in") || !strings.Contains(r.Output, "## a\nfinal answer") {
		t.Fatalf("task answer and persisted summary contracts diverged: task=%q record=%q", info.Output, r.Output)
	}
	if r.TaskID != info.ID || r.Status != RunSucceeded || r.Nodes["a"].Status != StatusSucceeded {
		t.Fatalf("task and persisted checkpoint diverged: %+v", r)
	}
	if strings.Contains(info.LiveView, "Read(example.go)") || !strings.Contains(info.LiveView, "1/1") {
		t.Fatalf("parent task should show the graph, not duplicate child activity: %s", info.LiveView)
	}
}

func TestResumeRetainsSavedFilenameName(t *testing.T) {
	wt := NewService(task.Default())
	wt.SetExecutor(&scriptedExecutor{})
	definition := "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n"
	if err := wt.runStore().Put(RunRecord{ID: "named", WorkerPolicies: map[string]string{"a": "cfg:"}, Name: "filename-name", Definition: definition}); err != nil {
		t.Fatal(err)
	}
	taskID, err := wt.ResumeRun("named", "", 120)
	if err != nil {
		t.Fatal(err)
	}
	if bg, ok := task.Default().Get(taskID); !ok || bg.GetStatus().AgentName != "Filename name" {
		t.Fatalf("resume lost the saved name: %+v", bg)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := wt.GetRun("named")
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == RunSucceeded {
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
	wt := NewService(task.Default())
	e := &activityExecutor{ready: make(chan struct{})}
	wt.SetExecutor(e)
	_, err := startInline(wt, "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n", nil, 120)
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
	if data, err := json.Marshal(r); err != nil || strings.Contains(string(data), "actual 1") {
		t.Fatalf("live activity was persisted into the run record: %s %v", data, err)
	}
	if _, err := wt.ResumeRun(r.ID, "", 120); err == nil {
		t.Fatal("duplicate resume accepted")
	}
	if err := wt.StopRun(r.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := wt.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == RunStopped {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stop timeout")
}

func TestFlowRetainsHistoricalRunDirectoryAndLockExclusion(t *testing.T) {
	root := t.TempDir()
	historical := NewStore(filepath.Join(root, ".san", "workflow-runs"))
	if err := historical.Put(RunRecord{ID: "existing", Status: RunRunning}); err != nil {
		t.Fatal(err)
	}
	release, err := historical.Acquire("existing")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	wt := NewService(task.Default())
	wt.SetSearchPaths([]string{filepath.Join(root, ".san", "workflows")})
	r, err := wt.GetRun("existing")
	if err != nil || r.Status != RunRunning {
		t.Fatalf("historical run is hidden or its active lock ignored: record=%+v err=%v", r, err)
	}
	if _, err := wt.runStore().Acquire("existing"); err == nil {
		t.Fatal("renamed tool bypassed the historical run lock")
	}
}

func TestRunInterruptionRequiresAbsentOwner(t *testing.T) {
	wt := NewService(task.Default())
	wt.SetSearchPaths([]string{filepath.Join(t.TempDir(), ".san", "workflows")})
	s := wt.runStore()
	if err := s.Put(RunRecord{ID: "run", Status: RunRunning}); err != nil {
		t.Fatal(err)
	}
	release, err := s.Acquire("run")
	if err != nil {
		t.Fatal(err)
	}
	r, err := wt.GetRun("run")
	if err != nil || r.Status != RunRunning {
		t.Fatalf("live %+v %v", r, err)
	}
	release()
	r, err = wt.GetRun("run")
	if err != nil || r.Status != RunInterrupted {
		t.Fatalf("dead %+v %v", r, err)
	}
}
func TestResumeSnapshotReusesSuccessfulBranch(t *testing.T) {
	wt := NewService(task.Default())
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
	b, parseErr := Parse(definition)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if err := wt.preflight(b); err != nil {
		t.Fatal(err)
	}
	r := RunRecord{ID: "retry", WorkerPolicies: b.workerPolicies, Definition: definition, Inputs: map[string]string{"base": "original"}, Status: RunFailed, Nodes: map[string]Checkpoint{"diff": {Outcome: Outcome{Status: StatusSucceeded, Output: "D"}, Attempt: 1}, "sec": {Outcome: Outcome{Status: StatusFailed, Error: "oops"}, Attempt: 1}, "perf": {Outcome: Outcome{Status: StatusSucceeded, Output: "P"}, Attempt: 1}}}
	if err := wt.runStore().Put(r); err != nil {
		t.Fatal(err)
	}
	wt = NewService(task.Default())
	wt.SetSearchPaths(paths)
	wt.SetExecutor(exec)
	_, err := wt.ResumeRun("retry", "", 120)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := wt.GetRun("retry")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == RunSucceeded {
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
	wt := NewService(task.Default())
	exec := &scriptedExecutor{}
	wt.SetExecutor(exec)
	p := t.TempDir()
	if err := os.Mkdir(filepath.Join(p, "blocked.json"), 0700); err != nil {
		t.Fatal(err)
	}
	wt.store = NewStore(p)
	w, err := Parse(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wt.startRecord(w, RunRecord{ID: "blocked", Definition: w.Source}, "test", 120); err == nil {
		t.Fatal("expected write failure")
	}
	if len(exec.reqs) != 0 {
		t.Fatal("untracked execution")
	}
}

func TestInspectAndStopAcceptTaskID(t *testing.T) {
	wt := NewService(task.Default())
	e := &activityExecutor{ready: make(chan struct{})}
	wt.SetExecutor(e)
	taskID, err := startInline(wt, "```mermaid\nflowchart LR\n a\n```\n## a\nwork\n", nil, 120)
	if err != nil {
		t.Fatal(err)
	}
	<-e.ready
	r, err := wt.GetRun(taskID)
	if err != nil || r.TaskID != taskID || r.ID == taskID {
		t.Fatalf("task ID did not resolve to its run: %+v %v", r, err)
	}
	if err := wt.StopRun(taskID); err != nil {
		t.Fatal(err)
	}
	bg, _ := task.Default().Get(taskID)
	if !bg.WaitForCompletion(5*time.Second) || !bg.GetStatus().Scheduler {
		t.Fatalf("scheduler task did not stop: %+v", bg.GetStatus())
	}
}
