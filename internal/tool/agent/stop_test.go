package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestAgentTool_StopSignalCancelsRunningTask(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-stop-1", "Explore Worker", "Long task", ctx, cancel)
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-stop-1")

	toolInst := NewAgentTool()

	result := toolInst.Execute(context.Background(), map[string]any{
		"signal":      "stop",
		"task_id":     "task-stop-1",
		"prompt":      "No longer needed",
		"description": "Stop worker",
	}, ".")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Task stopped successfully.") {
		t.Fatalf("unexpected output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "Reason: No longer needed") {
		t.Fatalf("cancellation reason missing: %s", result.Output)
	}
	if agentTask.IsRunning() {
		t.Fatal("task still running after stop signal")
	}
}

func TestAgentTool_StopSignalRejectsCompletedTask(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-stop-2", "Explore Worker", "Done task", ctx, cancel)
	agentTask.Complete(nil)
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-stop-2")

	toolInst := NewAgentTool()

	result := toolInst.Execute(context.Background(), map[string]any{
		"signal":      "stop",
		"task_id":     "task-stop-2",
		"prompt":      "Stop it",
		"description": "Stop worker",
	}, ".")

	if result.Success {
		t.Fatal("expected failure for completed task")
	}
	if !strings.Contains(result.Error, "already completed") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestAgentTool_StopSignalRequiresTaskID(t *testing.T) {
	toolInst := NewAgentTool()

	result := toolInst.Execute(context.Background(), map[string]any{
		"signal":      "stop",
		"prompt":      "Stop something",
		"description": "Stop worker",
	}, ".")

	if result.Success {
		t.Fatal("expected failure without task_id")
	}
	if !strings.Contains(result.Error, "task_id is required") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}
