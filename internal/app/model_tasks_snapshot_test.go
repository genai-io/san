package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/genai-io/san/internal/task"
)

type countedSnapshotTask struct {
	task.BackgroundTask
	statusReads, outputReads int
}

func (w *countedSnapshotTask) GetStatus() task.TaskInfo {
	w.statusReads++
	return w.BackgroundTask.GetStatus()
}

func (w *countedSnapshotTask) GetOutput() string {
	w.outputReads++
	return w.BackgroundTask.GetOutput()
}

func TestTaskRootSnapshotsReadEachWorkerOnce(t *testing.T) {
	m := interactionModel()
	m.services.sessionID = "current"
	agent := task.NewAgentTask("agent", "Reviewer", "Review", context.Background(), nil, "")
	agent.AppendOutput([]byte("agent output"))
	agent.Complete(nil)
	// Only exercise the Bash task's snapshot; no subprocess is started.
	bash := task.NewBashTask("bash", "printf output", "Output", &exec.Cmd{Process: &os.Process{}}, nil, "")
	bash.AppendOutput([]byte("bash output"))
	fallback := &taskViewerWorker{info: task.TaskInfo{ID: "fallback"}, output: "output-only worker"}
	foreign := &taskViewerWorker{info: task.TaskInfo{ID: "foreign", ParentSessionID: "other"}, output: "not displayed"}
	var workers []*countedSnapshotTask
	for _, worker := range []task.BackgroundTask{agent, bash, fallback, foreign} {
		counted := &countedSnapshotTask{BackgroundTask: worker}
		m.services.Task.RegisterTask(counted)
		counted.statusReads = 0 // Registration also reads status.
		workers = append(workers, counted)
	}
	items := m.taskSnapshots()
	outputs := make(map[string]string)
	for _, item := range items {
		outputs[item.ID] = item.Output
	}
	for id, want := range map[string]string{"agent": "agent output", "bash": "bash output", "fallback": "output-only worker"} {
		if outputs[id] != want {
			t.Fatalf("%s output = %q, want %q", id, outputs[id], want)
		}
	}
	if len(items) != 3 {
		t.Fatalf("foreign task was not filtered: %+v", items)
	}
	for i, worker := range workers {
		wantOutputReads := 0
		if i == 2 {
			wantOutputReads = 1
		}
		if worker.statusReads != 1 || worker.outputReads != wantOutputReads {
			t.Fatalf("worker %d read status %d times and output %d times", i, worker.statusReads, worker.outputReads)
		}
	}
}

func BenchmarkTaskSnapshotsCompletedOutput(b *testing.B) {
	for _, count := range []int{20, 100} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := interactionModel()
			data := bytes.Repeat([]byte("x"), 512*1024)
			for i := range count {
				worker := task.NewAgentTask(fmt.Sprint(i), "Reviewer", "Review", context.Background(), nil, "")
				worker.AppendOutput(data)
				worker.Complete(nil)
				m.services.Task.RegisterTask(worker)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.taskSnapshots()
			}
		})
	}
}
