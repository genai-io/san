package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

type continuationExecutor struct{ recordingExecutor }

func (e *continuationExecutor) RunBackground(req tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	e.runReq = req
	return tool.AgentTaskInfo{TaskID: req.ResumeTaskID, AgentName: req.Agent}, nil
}

func endedContinuationTask(t *testing.T, status task.TaskStatus) *task.AgentTask {
	t.Helper()
	ctx := task.WithSessionID(context.Background(), "parent")
	worker := task.Default().CreateAgentTask(task.NewID(), "reviewer", "Original review", ctx, func() {})
	worker.SetIdentity("reviewer", "child-session")
	worker.SetPermissionMode("explore")
	switch status {
	case task.StatusFailed:
		worker.Complete(errors.New("provider unavailable"))
	case task.StatusCompleted:
		worker.Complete(nil)
	case task.StatusStopped:
		worker.Complete(context.Canceled)
	case task.StatusKilled:
		_ = worker.Kill()
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	return worker
}

func TestAgentContinuationPassesOnlyIncrementalInstruction(t *testing.T) {
	worker := endedContinuationTask(t, task.StatusFailed)
	executor := &continuationExecutor{recordingExecutor: recordingExecutor{configOK: true}}
	agent := NewAgentTool()
	agent.SetExecutor(executor)
	ctx := task.WithSessionID(context.Background(), "parent")
	params := map[string]any{"resume_task_id": worker.GetID(), "description": "Continue review", "prompt": "Rerun only the focused validation; inspect existing changes first."}
	permission, err := agent.PreparePermission(ctx, params, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !permission.AgentMeta.Background || permission.AgentMeta.PermissionMode != "explore" || !strings.Contains(permission.Description, worker.GetID()) {
		t.Fatalf("continuation approval lost task/policy: %+v", permission)
	}
	result := agent.ExecuteApproved(ctx, params, ".")
	if !result.Success {
		t.Fatal(result.Error)
	}
	req := executor.runReq
	if req.ResumeTaskID != worker.GetID() || !req.Background || req.Prompt != params["prompt"] || req.Description != "Original review" || req.Agent != "reviewer" {
		t.Fatalf("continuation was reframed as new work: %+v", req)
	}
	if req.ResolvedAgentConfig != nil || !strings.Contains(result.Output, "original session") {
		t.Fatalf("continuation replaced original policy/session: req=%+v result=%+v", req, result)
	}
}

func TestAgentContinuationRejectsMalformedIdentity(t *testing.T) {
	agent := NewAgentTool()
	agent.SetExecutor(&continuationExecutor{})
	for _, value := range []any{42, true, []string{"task"}, map[string]any{"id": "task"}} {
		params := map[string]any{"resume_task_id": value, "prompt": "Continue", "description": "Follow up"}
		if result := agent.Execute(context.Background(), params, "."); result.Success || !strings.Contains(result.Error, "existing task ID string or null") {
			t.Fatalf("malformed continuation silently became new work: %+v", result)
		}
	}
}

func TestAgentContinuationRejectsUnsafeTargetsBeforeApproval(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusRunning, task.StatusStopped, task.StatusKilled} {
		t.Run(string(status), func(t *testing.T) {
			worker := endedContinuationTask(t, status)
			agent := NewAgentTool()
			agent.SetExecutor(&continuationExecutor{})
			params := map[string]any{"resume_task_id": worker.GetID(), "prompt": "Continue", "description": "Follow up"}
			if _, err := agent.PreparePermission(task.WithSessionID(context.Background(), "parent"), params, "."); err == nil {
				t.Fatal("unsafe continuation accepted for approval")
			}
			if result := agent.Execute(context.Background(), params, "."); result.Success {
				t.Fatal("unsafe continuation executed")
			}
		})
	}
	worker := endedContinuationTask(t, task.StatusCompleted)
	agent := NewAgentTool()
	agent.SetExecutor(&continuationExecutor{})
	params := map[string]any{"resume_task_id": worker.GetID(), "prompt": "Continue", "description": "Follow up"}
	if result := agent.Execute(task.WithSessionID(context.Background(), "other-parent"), params, "."); result.Success || !strings.Contains(result.Error, "different parent session") {
		t.Fatalf("cross-session continuation: %+v", result)
	}
	params["name"] = "different-agent"
	if result := agent.Execute(task.WithSessionID(context.Background(), "parent"), params, "."); result.Success || !strings.Contains(result.Error, "original agent") {
		t.Fatalf("continuation switched definitions: %+v", result)
	}
}
