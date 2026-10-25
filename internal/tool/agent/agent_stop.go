package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// AgentStopTool stops a running background subagent. Bash tasks deliberately
// stay outside this control surface: Bash reports its process-group ID, so the
// model can stop that command with Bash itself.
type AgentStopTool struct{}

var _ tool.PermissionAwareTool = (*AgentStopTool)(nil)

func NewAgentStopTool() *AgentStopTool { return &AgentStopTool{} }

func (t *AgentStopTool) Name() string        { return tool.ToolAgentStop }
func (t *AgentStopTool) Description() string { return "Stop a running background agent" }
func (t *AgentStopTool) Icon() string        { return tool.IconAgent }

func (t *AgentStopTool) RequiresPermission() bool { return true }

func (t *AgentStopTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	taskID, err := tool.RequireString(params, "task_id")
	if err != nil {
		return nil, err
	}
	if worker, exists := task.Default().Get(taskID); exists {
		info := worker.GetStatus()
		if owner := task.SessionIDFromContext(ctx); owner != "" && info.ParentSessionID != "" && owner != info.ParentSessionID {
			return nil, fmt.Errorf("agent task belongs to a different parent session")
		}
		params["_stopAttempt"] = info.Attempt
	}
	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: fmt.Sprintf("Stop background agent %s", taskID),
	}, nil
}

func (t *AgentStopTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params)
}

func (t *AgentStopTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params)
}

func (t *AgentStopTool) execute(ctx context.Context, params map[string]any) toolresult.ToolResult {
	start := time.Now()
	taskID, err := tool.RequireString(params, "task_id")
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	bgTask, found := task.Default().Get(taskID)
	if !found {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("agent task not found: %s", taskID))
	}
	if bgTask.GetType() != task.TaskTypeAgent {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("task %s is a background Bash command; stop its process group with Bash instead", taskID))
	}
	if owner := task.SessionIDFromContext(ctx); owner != "" && bgTask.GetStatus().ParentSessionID != "" && owner != bgTask.GetStatus().ParentSessionID {
		return toolresult.NewErrorResult(t.Name(), "agent task belongs to a different parent session")
	}
	// Permission approval and graceful escalation stay pinned to the selected
	// execution; an old stop request must not cancel a later continuation.
	if attempt, pinned := params["_stopAttempt"].(int); pinned {
		err = task.Default().KillAttempt(taskID, attempt)
	} else {
		err = task.Default().Kill(taskID)
	}
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("failed to stop agent task: %v", err))
	}

	info := bgTask.GetStatus()
	output := fmt.Sprintf("Agent stopped successfully.\nTask ID: %s\nAgent: %s\nSteps: %d\nStatus: %s",
		taskID, info.AgentName, info.StepCount, info.Status)
	if reason := tool.GetString(params, "reason"); reason != "" {
		output += "\nReason: " + reason
	}
	if info.Output != "" {
		output += fmt.Sprintf("\n\nOutput before stop:\n%s", info.Output)
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("Stopped: %s", taskID),
			Duration: time.Since(start),
		},
	}
}

func init() {
	tool.Register(NewAgentStopTool())
}
