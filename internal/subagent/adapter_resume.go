package subagent

import (
	"fmt"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

// ContinuationConfig projects the original execution policy for permission
// review. A reloaded definition or changed parent mode must not misrepresent
// what continuing an existing child session will actually execute.
func (a *ExecutorAdapter) ContinuationConfig(taskID string) (tool.AgentConfigInfo, error) {
	worker, exists := task.Default().Get(taskID)
	if !exists {
		return tool.AgentConfigInfo{}, fmt.Errorf("continuation task %q is unavailable", taskID)
	}
	agentTask, ok := worker.(*task.AgentTask)
	if !ok || agentTask.GetStatus().ParentSessionID != a.parentSessionID {
		return tool.AgentConfigInfo{}, fmt.Errorf("continuation task is not owned by this parent session")
	}
	state, ok := agentTask.LaunchState().(*continuationState)
	if !ok || state.cfg == nil || state.cfg.config == nil {
		return tool.AgentConfigInfo{}, fmt.Errorf("original continuation policy is unavailable; do not silently start a fresh agent")
	}
	if _, enabled := a.resolveAgentConfig(state.req.Agent); !enabled {
		return tool.AgentConfigInfo{}, fmt.Errorf("the original agent definition is now disabled")
	}
	info := ToAgentConfigInfo(state.cfg.config)
	info.Model = state.cfg.modelID
	info.PermissionMode = string(state.cfg.permMode)
	return info, nil
}
