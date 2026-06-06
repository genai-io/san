package subagent

import (
	"context"

	"github.com/genai-io/san/internal/tool"
)

// ExecutorAdapter adapts the Executor to implement tool.AgentExecutor
type ExecutorAdapter struct {
	*Executor
}

// NewExecutorAdapter creates a new adapter for the Executor
func NewExecutorAdapter(executor *Executor) *ExecutorAdapter {
	return &ExecutorAdapter{Executor: executor}
}

// Verify ExecutorAdapter implements tool.AgentExecutor
var _ tool.AgentExecutor = (*ExecutorAdapter)(nil)

// Run executes an agent and projects the rich AgentResult down to the
// tool-facing AgentExecResult (flattening token usage, dropping internal
// fields the tool layer does not need).
func (a *ExecutorAdapter) Run(ctx context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	result, err := a.Executor.Run(ctx, req)
	if err != nil {
		return nil, err
	}

	return &tool.AgentExecResult{
		AgentID:           result.AgentID,
		AgentName:         result.AgentName,
		OutputFile:        result.TranscriptPath,
		Model:             result.Model,
		Success:           result.Success,
		Content:           result.Content,
		StepCount:         result.StepCount,
		ToolUses:          result.ToolUses,
		TotalInputTokens:  result.TokenUsage.InputTokens,
		TotalOutputTokens: result.TokenUsage.OutputTokens,
		Duration:          result.Duration,
		Progress:          result.Progress,
		Error:             result.Error,
	}, nil
}

// RunBackground executes an agent in background
func (a *ExecutorAdapter) RunBackground(req tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	req.Background = true

	agentTask, err := a.Executor.RunBackground(req)
	if err != nil {
		return tool.AgentTaskInfo{}, err
	}

	return tool.AgentTaskInfo{
		TaskID:     agentTask.GetID(),
		AgentName:  agentTask.AgentName,
		OutputFile: agentTask.GetOutputFile(),
	}, nil
}

// GetParentModelID returns the parent conversation's model ID
func (a *ExecutorAdapter) GetParentModelID() string {
	return a.Executor.GetParentModelID()
}

// GetAgentConfig returns configuration for an agent type
// Returns false if agent is not found or is disabled
func (a *ExecutorAdapter) GetAgentConfig(agentType string) (tool.AgentConfigInfo, bool) {
	if !defaultRegistry.IsEnabled(agentType) {
		return tool.AgentConfigInfo{}, false
	}

	config, ok := defaultRegistry.Get(agentType)
	if !ok {
		return tool.AgentConfigInfo{}, false
	}

	return ToAgentConfigInfo(config), true
}

// ToAgentConfigInfo projects an agent definition into the display info shared by
// the Agent tool and the TUI agent selector.
func ToAgentConfigInfo(c *AgentConfig) tool.AgentConfigInfo {
	var tools []string
	if c.AllowTools != nil {
		tools = c.AllowTools.DisplayNames()
	}
	return tool.AgentConfigInfo{
		Name:           c.Name,
		Description:    c.Description,
		Color:          c.Color,
		Model:          c.Model,
		PermissionMode: string(c.PermissionMode),
		Tools:          tools,
		SourceFile:     c.SourceFile,
		Source:         c.Source,
	}
}
