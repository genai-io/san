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

// Run executes an agent and returns the result
func (a *ExecutorAdapter) Run(ctx context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	agentReq := AgentRequest{
		Agent:       req.Agent,
		Name:        req.Name,
		Prompt:      req.Prompt,
		Description: req.Description,
		Background:  req.Background,
		Model:       req.Model,
		MaxSteps:    req.MaxSteps,
		Mode:        req.Mode,
		ResumeID:    req.ResumeID,
		Isolation:   req.Isolation,
		OnQuestion:  req.OnQuestion,
	}

	if req.OnProgress != nil {
		agentReq.OnProgress = ProgressCallback(req.OnProgress)
	}

	result, err := a.Executor.Run(ctx, agentReq)
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
	agentReq := AgentRequest{
		Agent:       req.Agent,
		Name:        req.Name,
		Prompt:      req.Prompt,
		Description: req.Description,
		Background:  true,
		Model:       req.Model,
		MaxSteps:    req.MaxSteps,
		Mode:        req.Mode,
		ResumeID:    req.ResumeID,
		Isolation:   req.Isolation,
	}

	agentTask, err := a.Executor.RunBackground(agentReq)
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

	return tool.AgentConfigInfo{
		Name:           config.Name,
		Description:    config.Description,
		Color:          config.Color,
		Model:          config.Model,
		PermissionMode: string(config.PermissionMode),
		Tools:          config.AllowTools.DisplayNames(),
	}, true
}
