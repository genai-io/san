package flow

import (
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

func TestFlowCompletionWaitsForLeaseRelease(t *testing.T) {
	b, err := Parse("```mermaid\nflowchart LR\n  only\n```\n\n## only\nfinish\n")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewRunSet()
	s := NewScheduler(tasks)
	s.store = newRunStore(t.TempDir())
	s.SetExecutor(&scriptedExecutor{outputs: map[string]string{"only": "done"}})
	release, err := s.store.Acquire("completion")
	if err != nil {
		t.Fatal(err)
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	id, err := s.launchRecord(b, RunRecord{ID: "completion", Definition: b.Source}, "completion", func() {
		close(entered)
		<-proceed
		release()
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	bg, _ := tasks.Get(id)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not reach lease cleanup")
	}
	if !bg.IsRunning() {
		t.Fatal("task completed before releasing its executor lease")
	}
	unblock()
	waitFlowTask(t, bg)
	if _, err := s.ResumeRun("completion", "only", 100); err != nil {
		t.Fatalf("resume after completion: %v", err)
	}
	records, err := s.Runs()
	if err != nil {
		t.Fatal(err)
	}
	resumed, _ := tasks.Get(records[0].TaskID)
	waitFlowTask(t, resumed)
}
