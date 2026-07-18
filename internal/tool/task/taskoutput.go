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
	IconTaskOutput              = ">"
	recentLaunchPollingCooldown = 15 * time.Second
)

// TaskOutputTool retrieves output from background tasks.
// Aliases: AgentOutput (deprecated).
type TaskOutputTool struct{}

func (t *TaskOutputTool) Name() string { return "TaskOutput" }
func (t *TaskOutputTool) Description() string {
	return "Inspect a completed background task when the user explicitly asks for detailed output. Background agents automatically notify you on completion — do not use this to poll or check progress."
}
func (t *TaskOutputTool) Icon() string { return IconTaskOutput }

// Execute retrieves task output
func (t *TaskOutputTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
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

	// Default to a non-blocking status check so background tasks remain useful.
	block := false
	if b, ok := params["block"].(bool); ok {
		block = b
	}

	timeout := min(time.Duration(tool.GetFloat64(params, "timeout", 30000))*time.Millisecond, 600*time.Second)

	// Get task
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

	// If blocking, wait for completion
	if block && bgTask.IsRunning() {
		if !bgTask.WaitForCompletion(timeout) {
			// Task still running - return friendly status with options
			info := bgTask.GetStatus()
			output := formatTaskOutput(info, "still running")
			output += fmt.Sprintf("\nOptions:\n"+
				"  - Keep working; the background task may still complete on its own\n"+
				"  - Wait longer now: TaskOutput(task_id=\"%s\", block=true, timeout=60000)\n"+
				"  - Check status later: TaskOutput(task_id=\"%s\")\n"+
				"  - Stop: TaskStop(task_id=\"%s\")\n", taskID, taskID, taskID)

			if info.Output != "" {
				output += fmt.Sprintf("\nCurrent output:\n%s", info.Output)
			}

			return toolresult.ToolResult{
				Success: true,
				Output:  output,
				Metadata: toolresult.ResultMetadata{
					Title:    t.Name(),
					Icon:     t.Icon(),
					Subtitle: fmt.Sprintf("%s: still running", taskID),
					Duration: time.Since(start),
				},
			}
		}
	}

	// Get task status
	info := bgTask.GetStatus()
	if shouldDeferImmediatePolling(info, block) {
		return toolresult.ToolResult{
			Success: true,
			Output: fmt.Sprintf("TaskOutput deferred — this worker just started.\nTask ID: %s\nAgent: %s\nStatus: running\n\nYou will be automatically notified when this worker completes. Do not poll or check progress — continue with other work or respond to the user instead.",
				info.ID, info.AgentName),
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("%s: deferred", taskID),
				Duration: time.Since(start),
			},
		}
	}
	statusStr := formatStatusString(info)
	output := formatTaskOutput(info, statusStr)
	if !block && bgTask.IsRunning() {
		output += "Background task is still running.\n"
		output += fmt.Sprintf("Use TaskOutput(task_id=\"%s\", block=true) only when you want to wait here.\n", taskID)
	}

	if !info.EndTime.IsZero() {
		output += fmt.Sprintf("Duration: %v\n", info.EndTime.Sub(info.StartTime))
	}
	if info.Type == task.TaskTypeAgent && info.AgentSessionID != "" {
		output += fmt.Sprintf("Resume: SendMessage(task_id=\"%s\", message=\"...\") or Agent(resume=\"%s\", subagent_type=\"%s\", description=\"...\", prompt=\"...\")\n",
			info.ID, info.AgentSessionID, agentTypeForResume(info))
	}
	if info.Output != "" {
		output += fmt.Sprintf("\nOutput:\n%s", info.Output)
	}
	if info.Error != "" {
		output += fmt.Sprintf("\nError: %s", info.Error)
	}
	return toolresult.ToolResult{
		Success: info.Status != task.StatusFailed,
		Output:  output,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%s: %s", taskID, statusStr),
			Duration: time.Since(start),
		},
	}
}

// formatStatusString converts task status to a display string
func formatStatusString(info task.TaskInfo) string {
	switch info.Status {
	case task.StatusRunning:
		return "running"
	case task.StatusCompleted:
		return "completed"
	case task.StatusFailed:
		if info.Type == task.TaskTypeBash && info.ExitCode != 0 {
			return fmt.Sprintf("failed (exit code: %d)", info.ExitCode)
		}
		return "failed"
	case task.StatusKilled:
		return "killed"
	default:
		return "unknown"
	}
}

// formatTaskOutput builds the output string based on task type
func formatTaskOutput(info task.TaskInfo, status string) string {
	switch info.Type {
	case task.TaskTypeAgent:
		output := fmt.Sprintf("Agent: %s\nStatus: %s\nSteps: %d\nTokens: %d\n",
			info.AgentName, status, info.StepCount, info.TokenUsage)
		if info.AgentType != "" {
			output += fmt.Sprintf("AgentType: %s\n", info.AgentType)
		}
		if info.AgentSessionID != "" {
			output += fmt.Sprintf("AgentID: %s\n", info.AgentSessionID)
		}
		if info.OutputFile != "" {
			output += fmt.Sprintf("OutputFile: %s\n", info.OutputFile)
		}
		return output
	case task.TaskTypeBash:
		output := fmt.Sprintf("Task ID: %s\nStatus: %s\nPID: %d\n", info.ID, status, info.PID)
		if info.Command != "" {
			output += fmt.Sprintf("Command: %s\n", info.Command)
		}
		if info.OutputFile != "" {
			output += fmt.Sprintf("OutputFile: %s\n", info.OutputFile)
		}
		return output
	default:
		return fmt.Sprintf("Task ID: %s\nStatus: %s\n", info.ID, status)
	}
}

func agentTypeForResume(info task.TaskInfo) string {
	if info.AgentType != "" {
		return info.AgentType
	}
	return "general-purpose"
}

func shouldDeferImmediatePolling(info task.TaskInfo, block bool) bool {
	if block || info.Type != task.TaskTypeAgent || info.Status != task.StatusRunning || info.StartTime.IsZero() {
		return false
	}
	return time.Since(info.StartTime) < recentLaunchPollingCooldown
}

func init() {
	// TaskOutput is intentionally disabled. Background coordination relies on
	// task-notification re-entry and stable output files instead of polling.
}
