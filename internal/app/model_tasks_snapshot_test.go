package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/broker"
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

func TestTaskViewerSnapshotsOnlyOnRefreshBoundaries(t *testing.T) {
	m := interactionModel()
	m.services.sessionID = "current"
	worker := viewerWorker(m, "counted", "current", time.Now())
	counted := &countedSnapshotTask{BackgroundTask: worker}
	m.services.Task.RegisterTask(counted)
	m.openTasks()
	counted.statusReads, counted.outputReads = 0, 0

	for range 10 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m.Update(struct{}{}) // Unrelated stream/spinner events must not copy task output.
	}
	if counted.statusReads != 0 || counted.outputReads != 0 {
		t.Fatalf("unrelated updates recopied task output: status=%d output=%d", counted.statusReads, counted.outputReads)
	}
	m.Update(taskViewerTickMsg{sessionID: "foreign", viewID: m.tasks.pollID})
	m.Update(taskViewerTickMsg{sessionID: "current", viewID: m.tasks.pollID + 1})
	if counted.statusReads != 0 {
		t.Fatal("stale poll refreshed the current viewer")
	}
	m.Update(taskViewerTickMsg{sessionID: "current", viewID: m.tasks.pollID})
	if counted.statusReads != 1 || counted.outputReads != 1 {
		t.Fatalf("current poll did not capture exactly one snapshot: status=%d output=%d", counted.statusReads, counted.outputReads)
	}

	// Approval changes remain immediate, without waiting for a poll or keypress.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Update(conv.InteractionRequestMsg{Request: interactionApproval(m, ctx, worker.GetID())})
	if view := ansi.Strip(m.userInput.Tasks.Render()); !strings.Contains(view, "awaiting approval") {
		t.Fatalf("queued approval was not refreshed: %s", view)
	}
	cancel()
	m.Update(struct{}{})
	if view := ansi.Strip(m.userInput.Tasks.Render()); strings.Contains(view, "awaiting approval") {
		t.Fatalf("expired approval remained visible: %s", view)
	}

	worker.info.Status = task.StatusCompleted
	msg, _ := taskCompletionMessage(worker.info)
	m.backgroundNoticesHeld = true
	m.Update(mainNoticeMsg{notice: fromBrokerMessage(msg)})
	if view := ansi.Strip(m.userInput.Tasks.Render()); !strings.Contains(view, "[completed]") {
		t.Fatalf("completion did not refresh the viewer immediately: %s", view)
	}
	m.services.sessionID = "other"
	m.Update(struct{}{})
	if m.userInput.Tasks.IsActive() {
		t.Fatal("session switch left the old task viewer visible")
	}
}

func TestTaskViewerOrdinaryMailDoesNotRefreshSnapshots(t *testing.T) {
	m := interactionModel()
	m.services.sessionID = "current"
	worker := viewerWorker(m, "counted", "current", time.Now())
	counted := &countedSnapshotTask{BackgroundTask: worker}
	m.services.Task.RegisterTask(counted)
	m.openTasks()
	counted.statusReads, counted.outputReads = 0, 0
	worker.info.Description = "not-yet-polled"
	m.backgroundNoticesHeld = true
	m.Update(mainNoticeMsg{notice: fromBrokerMessage(broker.Message{From: "counted", Content: "Interim findings"})})
	// The conversation tracker may read status while committing the notice;
	// neither the viewer's output fallback nor its cached rows should refresh.
	if counted.outputReads != 0 || strings.Contains(m.userInput.Tasks.Render(), "not-yet-polled") {
		t.Fatal("ordinary worker mail refreshed the task viewer")
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
