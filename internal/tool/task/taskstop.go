package task

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const (
	IconTaskStop = "x"
)

// TaskStopTool stops a running background task.
// Aliases: AgentStop (deprecated).
type TaskStopTool struct{}

func (t *TaskStopTool) Name() string        { return "TaskStop" }
func (t *TaskStopTool) Description() string { return "Stops a running background task by its ID" }
func (t *TaskStopTool) Icon() string        { return IconTaskStop }

// Execute stops a running background task
func (t *TaskStopTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	taskID := tool.GetString(params, "task_id")
	if taskID == "" {
		return toolresult.ToolResult{
			Success: false,
			Error:   "task_id is required",
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	// Get task to check status before stopping
	bgTask, found := task.Default().Get(taskID)
	if !found {
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("task not found: %s", taskID),
			Metadata: toolresult.ResultMetadata{
				Title: t.Name(),
				Icon:  t.Icon(),
			},
		}
	}

	// Check if already completed
	if !bgTask.IsRunning() {
		info := bgTask.GetStatus()
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("task already completed with status: %s", info.Status),
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("Already: %s", info.Status),
			},
		}
	}

	// Get task info before stopping
	info := bgTask.GetStatus()

	// Stop the task using the interface method
	err := task.Default().Kill(taskID)
	duration := time.Since(start)

	if err != nil {
		return toolresult.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to stop task: %v", err),
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Duration: duration,
			},
		}
	}

	// Get final status
	finalInfo := bgTask.GetStatus()

	// Build output based on task type
	var output string
	switch info.Type {
	case task.TaskTypeBash:
		output = fmt.Sprintf("Task stopped successfully.\nTask ID: %s\nType: bash\nPID: %d\nStatus: %s",
			taskID, info.PID, finalInfo.Status)
	case task.TaskTypeAgent:
		output = fmt.Sprintf("Task stopped successfully.\nTask ID: %s\nType: agent\nAgent: %s\nSteps: %d\nStatus: %s",
			taskID, info.AgentName, info.StepCount, finalInfo.Status)
	default:
		output = fmt.Sprintf("Task stopped successfully.\nTask ID: %s\nStatus: %s",
			taskID, finalInfo.Status)
	}

	if finalInfo.Output != "" {
		output += fmt.Sprintf("\n\nOutput before stop:\n%s", finalInfo.Output)
	}

	return toolresult.ToolResult{
		Success: true,
		Output:  output,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("Stopped: %s", taskID),
			Duration: duration,
		},
	}
}

func init() {
	t := &TaskStopTool{}
	tool.Register(t)
	tool.Default().RegisterAlias("AgentStop", t)
}
