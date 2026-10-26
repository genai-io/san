package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// AgentTool spawns subagents to handle complex tasks.
// It implements PermissionAwareTool to require user confirmation.
type AgentTool struct {
	executor tool.AgentExecutor
}

// NewAgentTool creates a new AgentTool
func NewAgentTool() *AgentTool {
	return &AgentTool{}
}

func (t *AgentTool) Name() string        { return "Agent" }
func (t *AgentTool) Description() string { return "Launch a subagent to handle complex tasks" }
func (t *AgentTool) Icon() string        { return tool.IconAgent }

// RequiresPermission returns true - Agent always requires permission
func (t *AgentTool) RequiresPermission() bool {
	return true
}

// SetExecutor sets the agent executor
func (t *AgentTool) SetExecutor(executor tool.AgentExecutor) {
	t.executor = executor
}

// PreparePermission prepares a permission request with agent metadata
func (t *AgentTool) PreparePermission(ctx context.Context, params map[string]any, cwd string) (*perm.PermissionRequest, error) {
	agentName := tool.GetString(params, "name")

	prompt, err := tool.RequireString(params, "prompt")
	if err != nil {
		return nil, err
	}

	description := tool.GetString(params, "description")
	if description == "" {
		description = "Run agent task"
	}

	runBackground := tool.GetBool(params, "run_in_background")
	if err := validateAgentParams(params); err != nil {
		return nil, err
	}

	// Check if executor is configured
	if t.executor == nil {
		return nil, fmt.Errorf("agent executor not configured")
	}

	resume, err := resumeTarget(ctx, params)
	if err != nil {
		return nil, err
	}
	if resume.ID != "" {
		agentName, description, runBackground = resume.AgentName, resume.Description, true
		params["_resumeAttempt"] = resume.Attempt
	}

	// Resolve the agent definition and carry the exact config into execution so a
	// registry reload while approval is pending cannot change what was approved.
	// A continuation is pinned to the original worker policy by the executor.
	var config tool.AgentConfigInfo
	if resolver, supported := t.executor.(interface {
		ContinuationConfig(string) (tool.AgentConfigInfo, error)
	}); resume.ID != "" && supported {
		config, err = resolver.ContinuationConfig(resume.ID)
		if err != nil {
			return nil, err
		}
	} else {
		selected, resolvedConfig, ok := t.executor.ResolveAgentSelection(agentName)
		if !ok {
			return nil, fmt.Errorf("unknown or disabled agent: %s", agentName)
		}
		config = selected
		if resume.ID == "" {
			params["_resolvedAgentConfig"] = resolvedConfig
		} else {
			config.PermissionMode = resume.PermissionMode
		}
	}

	// Determine effective model for permission display.
	effectiveModel := config.Model
	if effectiveModel == "inherit" {
		effectiveModel = ""
	}
	if effectiveModel == "" {
		effectiveModel = t.executor.ParentModelID()
	}
	if effectiveModel == "" {
		effectiveModel = "inherit"
	}

	// Build description
	displayName := displayAgentLabel(config.Name)
	desc := fmt.Sprintf("Spawn %s agent: %s", displayName, description)
	if resume.ID != "" {
		desc = fmt.Sprintf("Continue %s agent task %s in its original session: %s", displayName, resume.ID, description)
	}
	if runBackground {
		desc += " (background)"
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: desc,
		AgentMeta: &perm.AgentMetadata{
			AgentName:      displayName,
			Description:    config.Description,
			Model:          effectiveModel,
			PermissionMode: config.PermissionMode,
			Tools:          config.Tools,
			Prompt:         prompt,
			Background:     runBackground,
		},
	}, nil
}

// Reject removed selectors rather than silently widening an old read-only request.
func validateAgentParams(params map[string]any) error {
	if value := params["resume_task_id"]; value != nil {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("resume_task_id must be an existing task ID string or null; omit it for new work")
		}
	}
	for _, field := range []string{"mode", "model"} {
		if _, exists := params[field]; exists {
			return fmt.Errorf("Agent does not accept %q; worker configuration comes from session settings or its definition", field)
		}
	}
	return nil
}

// requestedResumeTaskID normalizes explicit no-session placeholders used by
// callers that populate optional schema fields. It never turns an unknown real
// task ID into new work, and the field is never interpreted as a file path.
func requestedResumeTaskID(params map[string]any) string {
	id := strings.TrimSpace(tool.GetString(params, "resume_task_id"))
	switch strings.ToLower(id) {
	case "", "new", "/dev/null":
		return ""
	default:
		return id
	}
}

// resumeTarget validates identity without changing task state. The executor
// atomically claims the next attempt after permission approval.
func resumeTarget(ctx context.Context, params map[string]any) (task.TaskInfo, error) {
	id := requestedResumeTaskID(params)
	if id == "" {
		return task.TaskInfo{}, nil
	}
	worker, ok := task.Default().Get(id)
	if !ok {
		return task.TaskInfo{}, fmt.Errorf("task %q is not available for continuation. For new work, omit resume_task_id or set it to null; for continuation, use the exact Task ID returned by Agent, not an agent name or file path. No new conversation was started", id)
	}
	info := worker.GetStatus()
	if sessionID := task.SessionIDFromContext(ctx); sessionID != "" && info.ParentSessionID != "" && info.ParentSessionID != sessionID {
		return task.TaskInfo{}, fmt.Errorf("task %q belongs to a different parent session", id)
	}
	if info.Type != task.TaskTypeAgent {
		return task.TaskInfo{}, fmt.Errorf("task %q is not an agent session; inspect the command failure and restart it explicitly if safe", id)
	}
	if info.Status == task.StatusRunning {
		return task.TaskInfo{}, fmt.Errorf("task %q is already running; use SendMessage to add instructions", id)
	}
	if info.Status == task.StatusStopped || info.Status == task.StatusKilled {
		return task.TaskInfo{}, fmt.Errorf("task %q was deliberately stopped; do not automatically resume it", id)
	}
	if !task.RecoveryBudgetAvailable(info.Attempts) {
		return task.TaskInfo{}, fmt.Errorf("task %q exhausted its corrective recovery budget; report the blocker and choose a new plan", id)
	}
	if name := strings.TrimSpace(tool.GetString(params, "name")); name != "" && name != info.AgentName {
		return task.TaskInfo{}, fmt.Errorf("continuation retains task %q's original agent; omit name instead of switching definitions", id)
	}
	return info, nil
}

// ExecuteApproved executes the agent after user approval
func (t *AgentTool) ExecuteApproved(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params, cwd)
}

// Execute implements the Tool interface
func (t *AgentTool) Execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	return t.execute(ctx, params, cwd)
}

// execute is the internal implementation. Subagents cannot reach this: the
// Agent tool is parent-only (excluded from every subagent's tool set), which
// is what keeps the model flat — main spawns workers, workers do not.
func (t *AgentTool) execute(ctx context.Context, params map[string]any, cwd string) toolresult.ToolResult {
	start := time.Now()

	agentName := tool.GetString(params, "name")

	prompt := tool.GetString(params, "prompt")
	if prompt == "" {
		return toolresult.NewErrorResult(t.Name(), "prompt is required")
	}

	description := tool.GetString(params, "description")
	runBackground := tool.GetBool(params, "run_in_background")
	if err := validateAgentParams(params); err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}

	var onActivity tool.ActivityFunc
	if cb, ok := params["_onActivity"].(tool.ActivityFunc); ok {
		onActivity = cb
	}
	var onQuestion tool.AskQuestionFunc
	if cb, ok := params["_onQuestion"].(tool.AskQuestionFunc); ok {
		onQuestion = cb
	}

	maxSteps := tool.GetInt(params, "max_steps", 0)

	// Check executor
	if t.executor == nil {
		return toolresult.NewErrorResult(t.Name(), "agent executor not configured")
	}

	resume, err := resumeTarget(ctx, params)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), err.Error())
	}
	if resume.ID != "" {
		if approved, pinned := params["_resumeAttempt"].(int); pinned && approved != resume.Attempt {
			return toolresult.NewErrorResult(t.Name(), "the selected execution changed while continuation approval was pending; inspect the current task before continuing")
		}
		agentName, description, runBackground = resume.AgentName, resume.Description, true
	}

	resolvedAgentConfig, _ := params["_resolvedAgentConfig"]
	// New work receives a full brief. A continuation receives only the new
	// instruction; the executor restores the original child conversation.
	req := tool.AgentExecRequest{
		ResumeTaskID:        resume.ID,
		ResumeAttempt:       resume.Attempt,
		ParentToolCallID:    core.ToolCallIDFromContext(ctx),
		Agent:               agentName,
		ResolvedAgentConfig: resolvedAgentConfig,
		Prompt:              prompt,
		Description:         description,
		Background:          runBackground,
		MaxSteps:            maxSteps,
		OnActivity:          onActivity,
		OnQuestion:          onQuestion,
	}

	// Handle background execution
	if runBackground {
		taskInfo, err := t.executor.RunBackground(req)
		if err != nil {
			return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("failed to start background agent: %v", err))
		}

		verb := "started"
		if resume.ID != "" {
			verb = "continued in its original session"
		}
		output := fmt.Sprintf("Agent %s in background.\nTask ID: %s\nDescription: %s",
			verb, taskInfo.TaskID, description)
		duration := time.Since(start)
		return toolresult.ToolResult{
			Success: true,
			Details: toolresult.AgentDetails{TaskID: taskInfo.TaskID},
			Output:  output + tool.BackgroundLaunchSuffix,
			HookResponse: map[string]any{
				"backgroundTask": map[string]any{
					"taskId":      taskInfo.TaskID,
					"agentName":   agentName,
					"description": description,
					"outputFile":  taskInfo.OutputFile,
					"toolName":    t.Name(),
				},
			},
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("[background] %s: %s", displayAgentLabel(agentName), taskInfo.TaskID),
				Duration: duration,
			},
		}
	}

	// Foreground execution
	result, err := t.executor.Run(ctx, req)
	if err != nil && result == nil {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("agent execution failed: %v", err))
	}
	if result == nil {
		return toolresult.NewErrorResult(t.Name(), "agent returned no result")
	}
	if err != nil {
		result.Success = false
		if result.Error == "" {
			result.Error = err.Error()
		}
	}

	duration := time.Since(start)

	displayName := displayAgentLabel(agentName)
	agentDuration := result.Duration
	if agentDuration == 0 {
		agentDuration = duration
	}
	status := "finished"
	if !result.Success {
		status = "failed"
	}
	return toolresult.ToolResult{
		Success:      result.Success,
		Output:       formatForegroundAgentResult(result),
		Error:        result.Error,
		HookResponse: buildAgentHookResponse(result, agentName, prompt),
		Details: toolresult.AgentDetails{
			Model: result.Model, ToolUses: result.ToolUses,
			InputTokens: result.TotalInputTokens, OutputTokens: result.TotalOutputTokens,
			Duration: agentDuration, Activity: result.Activity, Error: result.Error,
			Stopped: errors.Is(err, context.Canceled),
		},
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%s: %s (%d steps)", displayName, status, result.StepCount),
			Duration: duration,
		},
	}
}

func displayAgentLabel(name string) string {
	if name != "" {
		return name
	}
	return "Agent"
}

// buildAgentHookResponse creates a structured response for PostToolUse hooks.
func buildAgentHookResponse(result *tool.AgentExecResult, agentName, prompt string) map[string]any {
	status := "completed"
	if !result.Success {
		status = "error"
	}

	return map[string]any{
		"agentId":           result.AgentID,
		"agentName":         agentName,
		"outputFile":        result.OutputFile,
		"content":           result.Content,
		"status":            status,
		"prompt":            prompt,
		"totalDurationMs":   result.Duration.Milliseconds(),
		"totalToolUseCount": result.ToolUses,
		"usage": map[string]any{
			"total_input_tokens":  result.TotalInputTokens,
			"total_output_tokens": result.TotalOutputTokens,
		},
	}
}

func init() {
	tool.Register(NewAgentTool())
}
