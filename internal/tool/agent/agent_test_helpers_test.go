package agent

import (
	"context"

	"github.com/genai-io/gen-code/internal/tool"
)

type stubSendMessageExecutor struct {
	lastRun           tool.AgentExecRequest
	lastBackgroundRun tool.AgentExecRequest
	runResult         *tool.AgentExecResult
	backgroundResult  tool.AgentTaskInfo
}

func (s *stubSendMessageExecutor) Run(ctx context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	s.lastRun = req
	if s.runResult == nil {
		s.runResult = &tool.AgentExecResult{
			AgentID:     "agent-resumed-2",
			AgentName:   "Explore",
			Model:       "sonnet",
			Success:     true,
			Content:     "done",
			StepCount:   2,
			ToolUses:    1,
			TotalTokens: 42,
		}
	}
	return s.runResult, nil
}

func (s *stubSendMessageExecutor) RunBackground(req tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	s.lastBackgroundRun = req
	if s.backgroundResult.TaskID == "" {
		s.backgroundResult = tool.AgentTaskInfo{TaskID: "bg-continued-1", AgentName: "Explore"}
	}
	return s.backgroundResult, nil
}

func (s *stubSendMessageExecutor) GetAgentConfig(agentType string) (tool.AgentConfigInfo, bool) {
	return tool.AgentConfigInfo{
		Name:           agentType,
		Description:    "test agent",
		PermissionMode: "default",
		Tools:          []string{"Read"},
	}, true
}

func (s *stubSendMessageExecutor) GetParentModelID() string {
	return "sonnet"
}
