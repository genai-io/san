package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/tool/toolresult"
)

const backgroundLaunchSuffix = "\n\nThe agent is working in the background. You will be notified automatically when it completes.\nBriefly tell the user what you launched and end your response. Do not generate any other text — agent results will arrive in a subsequent message."

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
	requestModel := tool.GetString(params, "model")

	mode, err := parseAndValidateRequestMode(params)
	if err != nil {
		return nil, err
	}

	// Check if executor is configured
	if t.executor == nil {
		return nil, fmt.Errorf("agent executor not configured")
	}

	// Resolve the selected custom agent config, or the implicit default when name
	// is omitted. Carry the exact config into execution so a registry reload while
	// approval is pending cannot change what the user approved.
	config, resolvedConfig, ok := t.executor.ResolveAgentSelection(agentName)
	if !ok {
		return nil, fmt.Errorf("unknown or disabled custom agent: %s", agentName)
	}
	params["_resolvedAgentConfig"] = resolvedConfig

	// Determine effective model for permission display.
	effectiveModel := requestModel
	if effectiveModel == "" && config.Model != "" && config.Model != "inherit" {
		effectiveModel = config.Model
	}
	if effectiveModel == "" {
		effectiveModel = t.executor.GetParentModelID()
	}
	if effectiveModel == "" {
		effectiveModel = "inherit"
	}

	// Build description
	desc := fmt.Sprintf("Spawn %s agent: %s", config.Name, description)
	if runBackground {
		desc += " (background)"
	}

	return &perm.PermissionRequest{
		ID:          tool.GenerateRequestID(),
		ToolName:    t.Name(),
		Description: desc,
		AgentMeta: &perm.AgentMetadata{
			AgentName:      config.Name,
			Description:    config.Description,
			Model:          effectiveModel,
			PermissionMode: effectivePermissionMode(mode, config),
			Tools:          config.Tools,
			Prompt:         prompt,
			Background:     runBackground,
		},
	}, nil
}

// effectivePermissionMode returns the mode the run will actually use — the
// request's mode override when present, the agent config's mode otherwise —
// so the permission dialog shows what the user is approving.
func effectivePermissionMode(mode string, config tool.AgentConfigInfo) string {
	if mode != "" && mode != "default" {
		return mode
	}
	return config.PermissionMode
}

func parseAndValidateRequestMode(params map[string]any) (string, error) {
	mode := tool.GetString(params, "mode")
	switch mode {
	case "", "default", "explore", "edit":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid agent mode %q: must be explore, edit, or default", mode)
	}
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
	model := tool.GetString(params, "model")
	mode, err := parseAndValidateRequestMode(params)
	if err != nil {
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

	resolvedAgentConfig, _ := params["_resolvedAgentConfig"]
	// Build request — subagents always start with fresh context. Parent agent
	// is responsible for putting all needed background into Prompt.
	req := tool.AgentExecRequest{
		Agent:               agentName,
		ResolvedAgentConfig: resolvedAgentConfig,
		Prompt:              prompt,
		Description:         description,
		Background:          runBackground,
		Model:               model,
		MaxSteps:            maxSteps,
		Mode:                mode,
		OnActivity:          onActivity,
		OnQuestion:          onQuestion,
	}

	// Handle background execution
	if runBackground {
		taskInfo, err := t.executor.RunBackground(req)
		if err != nil {
			return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("failed to start background agent: %v", err))
		}

		duration := time.Since(start)
		return toolresult.ToolResult{
			Success: true,
			Output: fmt.Sprintf("Agent started in background.\nTask ID: %s\nAgent: %s\nDescription: %s"+backgroundLaunchSuffix,
				taskInfo.TaskID, taskInfo.AgentName, description),
			HookResponse: map[string]any{
				"backgroundTask": map[string]any{
					"taskId":      taskInfo.TaskID,
					"agentName":   taskInfo.AgentName,
					"agentType":   agentTypeLabel(agentName),
					"description": description,
					"outputFile":  taskInfo.OutputFile,
					"toolName":    t.Name(),
				},
			},
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("[background] %s: %s", agentTypeLabel(agentName), taskInfo.TaskID),
				Duration: duration,
			},
		}
	}

	// Foreground execution
	result, err := t.executor.Run(ctx, req)
	if err != nil {
		return toolresult.NewErrorResult(t.Name(), fmt.Sprintf("agent execution failed: %v", err))
	}

	duration := time.Since(start)

	agentName = agentTypeLabel(agentName)
	if !result.Success {
		hookResponse := buildAgentHookResponse(result, agentName, prompt)
		return toolresult.ToolResult{
			Success:      false,
			Output:       result.Content,
			Error:        result.Error,
			HookResponse: hookResponse,
			Metadata: toolresult.ResultMetadata{
				Title:    t.Name(),
				Icon:     t.Icon(),
				Subtitle: fmt.Sprintf("%s: failed", agentName),
				Duration: duration,
			},
		}
	}

	hookResponse := buildAgentHookResponse(result, agentName, prompt)
	return toolresult.ToolResult{
		Success:      true,
		Output:       formatForegroundAgentResult(agentName, result, duration),
		HookResponse: hookResponse,
		Metadata: toolresult.ResultMetadata{
			Title:    t.Name(),
			Icon:     t.Icon(),
			Subtitle: fmt.Sprintf("%s: done (%d steps)", agentName, result.StepCount),
			Duration: duration,
		},
	}
}

func agentTypeLabel(name string) string {
	if name == "" {
		return "subagent"
	}
	return name
}

// buildAgentHookResponse creates a CC-compatible structured response for PostToolUse hooks.
func buildAgentHookResponse(result *tool.AgentExecResult, agentType, prompt string) map[string]any {
	status := "completed"
	if !result.Success {
		status = "error"
	}

	return map[string]any{
		"agentId":           result.AgentID,
		"agentType":         agentType,
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
