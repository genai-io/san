package task

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

func TestTaskOutputTool_StillRunning(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("test-agent-123", "Explore", "Test task", ctx, cancel)
	agentTask.UpdateProgress(5, 1000)
	agentTask.AppendOutput([]byte("Some partial output\n"))

	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("test-agent-123")

	tool := &TaskOutputTool{}
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "test-agent-123",
		"block":   true,
		"timeout": float64(100),
	}, ".")

	if !result.Success {
		t.Errorf("Expected Success=true for still-running task, got false. Error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "still running") {
		t.Errorf("Expected 'still running' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "Steps: 5") {
		t.Errorf("Expected 'Steps: 5' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "Options:") {
		t.Errorf("Expected 'Options:' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "TaskOutput") {
		t.Errorf("Expected TaskOutput suggestion in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "TaskStop") {
		t.Errorf("Expected TaskStop suggestion in output, got: %s", result.Output)
	}
}

func TestTaskOutputTool_Completed(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())

	agentTask := task.NewAgentTask("test-agent-456", "Explore", "Test task", ctx, cancel)
	agentTask.SetIdentity("Explore", "agent-session-456")
	agentTask.SetOutputFile("/tmp/transcripts/agent-session-456.jsonl")
	agentTask.UpdateProgress(10, 2000)
	agentTask.AppendOutput([]byte("Final output\n"))
	agentTask.Complete(nil)
	cancel()

	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("test-agent-456")

	tool := &TaskOutputTool{}
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "test-agent-456",
		"block":   true,
		"timeout": float64(1000),
	}, ".")

	if !result.Success {
		t.Errorf("Expected Success=true for completed task, got false. Error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "completed") {
		t.Errorf("Expected 'completed' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "Steps: 10") {
		t.Errorf("Expected 'Steps: 10' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "AgentID: agent-session-456") {
		t.Errorf("Expected resumable AgentID in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "OutputFile: /tmp/transcripts/agent-session-456.jsonl") {
		t.Errorf("Expected output file path in output, got: %s", result.Output)
	}

	// A finished worker cannot be messaged or resumed, so the output must not
	// suggest SendMessage or Agent(resume=...) — both were removed and would
	// fail or mislead if the model followed them.
	if strings.Contains(result.Output, "SendMessage") || strings.Contains(result.Output, "resume=") {
		t.Errorf("completed task must not suggest messaging/resuming a finished worker, got: %s", result.Output)
	}
}

func TestTaskOutputTool_NotFound(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	tool := &TaskOutputTool{}
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "nonexistent-task",
		"block":   false,
	}, ".")

	if result.Success {
		t.Error("Expected Success=false for nonexistent task")
	}

	if !strings.Contains(result.Error, "not found") {
		t.Errorf("Expected 'not found' in error, got: %s", result.Error)
	}
}

func TestTaskOutputTool_NonBlocking(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("test-agent-789", "Explore", "Test task", ctx, cancel)
	agentTask.UpdateProgress(3, 500)

	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("test-agent-789")

	tool := &TaskOutputTool{}
	start := time.Now()
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "test-agent-789",
		"block":   false,
	}, ".")
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Non-blocking call took too long: %v", elapsed)
	}

	if !result.Success {
		t.Errorf("Expected Success=true, got false. Error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "running") {
		t.Errorf("Expected 'running' in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "TaskOutput deferred") {
		t.Errorf("Expected deferred polling hint in output, got: %s", result.Output)
	}
}

func TestTaskOutputTool_DefaultsToNonBlocking(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("test-agent-default", "Explore", "Test task", ctx, cancel)
	agentTask.UpdateProgress(2, 300)

	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("test-agent-default")

	tool := &TaskOutputTool{}
	start := time.Now()
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "test-agent-default",
	}, ".")
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Default TaskOutput call should be non-blocking, took %v", elapsed)
	}

	if !result.Success {
		t.Errorf("Expected Success=true, got false. Error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "running") {
		t.Errorf("Expected 'running' in output, got: %s", result.Output)
	}
}

func TestTaskOutputTool_AllowsOlderRunningTaskInspection(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("test-agent-older", "Explore", "Test task", ctx, cancel)
	agentTask.UpdateProgress(7, 900)
	agentTask.StartTime = time.Now().Add(-recentLaunchPollingCooldown - time.Second)

	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("test-agent-older")

	tool := &TaskOutputTool{}
	result := tool.Execute(context.Background(), map[string]any{
		"task_id": "test-agent-older",
	}, ".")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Background task is still running.") {
		t.Fatalf("expected normal running output for older task, got: %s", result.Output)
	}
}
