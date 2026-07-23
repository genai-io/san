package agent

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestAgentStopCancelsRunningAgent(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-stop-1", "Explore Worker", "Long task", ctx, cancel)
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-stop-1")
	go func() {
		<-ctx.Done()
		agentTask.Complete(ctx.Err())
	}()

	toolInst := NewAgentStopTool()

	result := toolInst.Execute(context.Background(), map[string]any{
		"task_id": "task-stop-1",
		"reason":  "No longer needed",
	}, ".")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Agent stopped successfully.") {
		t.Fatalf("unexpected output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Reason: No longer needed") {
		t.Fatalf("cancellation reason missing: %s", result.Output)
	}
	if agentTask.IsRunning() {
		t.Fatal("task still running after stop signal")
	}
}

func TestAgentStopRejectsCompletedAgent(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-stop-2", "Explore Worker", "Done task", ctx, cancel)
	agentTask.Complete(nil)
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-stop-2")

	toolInst := NewAgentStopTool()

	result := toolInst.Execute(context.Background(), map[string]any{
		"task_id": "task-stop-2",
	}, ".")

	if result.Success {
		t.Fatal("expected failure for completed task")
	}
	if !strings.Contains(result.Error, "already completed") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestAgentStopRequiresTaskID(t *testing.T) {
	toolInst := NewAgentStopTool()

	result := toolInst.Execute(context.Background(), map[string]any{}, ".")

	if result.Success {
		t.Fatal("expected failure without task_id")
	}
	if !strings.Contains(result.Error, "task_id is required") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestAgentStopRejectsBashTask(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	cmd := exec.Command("bash", "-c", "sleep 60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bash task: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	bashTask := task.Default().CreateBashTask(cmd, "sleep 60", "Long command", func() {})
	defer task.Default().Remove(bashTask.GetID())

	result := NewAgentStopTool().Execute(context.Background(), map[string]any{
		"task_id": bashTask.GetID(),
	}, ".")

	if result.Success {
		t.Fatal("expected bash task rejection")
	}
	if !strings.Contains(result.Error, "stop its process group with Bash") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}
