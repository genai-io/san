package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/v2/internal/task"
)

func TestSendMessageTool_ResumesCompletedTask(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-sendmessage-1", "Explore Worker", "Initial task", ctx, cancel)
	agentTask.SetIdentity("Explore", "agent-session-321")
	agentTask.Complete(nil)
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-sendmessage-1")

	executor := &stubSendMessageExecutor{}
	toolInst := NewSendMessageTool()
	toolInst.SetExecutor(executor)

	result := toolInst.Execute(context.Background(), map[string]any{
		"task_id":     "task-sendmessage-1",
		"message":     "Please verify the earlier finding",
		"description": "Verify finding",
	}, ".")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if executor.lastRun.ResumeID != "agent-session-321" {
		t.Fatalf("ResumeID = %q, want %q", executor.lastRun.ResumeID, "agent-session-321")
	}
	if executor.lastRun.Prompt != "Please verify the earlier finding" {
		t.Fatalf("Prompt = %q", executor.lastRun.Prompt)
	}
}

func TestSendMessageTool_RejectsRunningTask(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentTask := task.NewAgentTask("task-sendmessage-2", "Explore Worker", "Initial task", ctx, cancel)
	agentTask.SetIdentity("Explore", "agent-session-654")
	task.Default().RegisterTask(agentTask)
	defer task.Default().Remove("task-sendmessage-2")

	toolInst := NewSendMessageTool()
	toolInst.SetExecutor(&stubSendMessageExecutor{})

	result := toolInst.Execute(context.Background(), map[string]any{
		"task_id": "task-sendmessage-2",
		"message": "Keep going",
	}, ".")

	if result.Success {
		t.Fatalf("expected error for running task, got success")
	}
	if !strings.Contains(result.Error, "still running") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

func TestSendMessageTool_BackgroundMessageByAgentID(t *testing.T) {
	executor := &stubSendMessageExecutor{}
	toolInst := NewSendMessageTool()
	toolInst.SetExecutor(executor)

	result := toolInst.Execute(context.Background(), map[string]any{
		"agent_id":          "agent-session-777",
		"subagent_type":     "Explore",
		"message":           "Retry with narrower scope",
		"description":       "Retry audit",
		"run_in_background": true,
	}, ".")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if executor.lastBackgroundRun.ResumeID != "agent-session-777" {
		t.Fatalf("ResumeID = %q, want %q", executor.lastBackgroundRun.ResumeID, "agent-session-777")
	}
	if executor.lastBackgroundRun.Prompt != "Retry with narrower scope" {
		t.Fatalf("Prompt = %q", executor.lastBackgroundRun.Prompt)
	}
	if !strings.Contains(result.Output, "Message sent to worker in background.") {
		t.Fatalf("unexpected output: %s", result.Output)
	}
	if strings.Contains(result.Output, "Use TaskOutput") {
		t.Fatalf("should not encourage immediate TaskOutput polling: %s", result.Output)
	}
}

func TestSendMessageTool_RequiresAgentTypeForDirectAgentID(t *testing.T) {
	toolInst := NewSendMessageTool()
	toolInst.SetExecutor(&stubSendMessageExecutor{})

	result := toolInst.Execute(context.Background(), map[string]any{
		"agent_id": "agent-session-999",
		"message":  "Continue",
	}, ".")

	if result.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(result.Error, "subagent_type is required") {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}
