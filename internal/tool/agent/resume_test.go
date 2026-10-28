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

func TestAgentRejectsRemovedContinuationInput(t *testing.T) {
	agent := NewAgentTool()
	executor := &continuationExecutor{}
	agent.SetExecutor(executor)
	for _, value := range []any{nil, "", "new", "/dev/null", "existing-id", 42} {
		params := map[string]any{"resume_task_id": value, "prompt": "Continue", "description": "Follow up"}
		if result := agent.Execute(context.Background(), params, "."); result.Success || !strings.Contains(result.Error, "use SendMessage") || executor.runReq.Prompt != "" {
			t.Fatalf("removed input created work: %+v", result)
		}
	}
}
