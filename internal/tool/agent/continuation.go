package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/gen-code/internal/task"
	"github.com/genai-io/gen-code/internal/tool"
	"github.com/genai-io/gen-code/internal/tool/toolresult"
)

type continuedAgentTarget struct {
	taskID    string
	agentID   string
	agentType string
	name      string
}

func resolveContinuationTarget(params map[string]any) (continuedAgentTarget, error) {
	taskID := tool.GetString(params, "task_id")
	agentID := tool.GetString(params, "agent_id")
	agentType := tool.GetString(params, "subagent_type")

	if taskID == "" && agentID == "" {
		return continuedAgentTarget{}, fmt.Errorf("task_id or agent_id is required")
	}

	target := continuedAgentTarget{
		taskID:    taskID,
		agentID:   agentID,
		agentType: agentType,
	}

	if taskID == "" {
		if target.agentType == "" {
			return continuedAgentTarget{}, fmt.Errorf("subagent_type is required when continuing by agent_id")
		}
		return target, nil
	}

	bgTask, found := task.Default().Get(taskID)
	if !found {
		return continuedAgentTarget{}, fmt.Errorf("task not found: %s", taskID)
	}

	info := bgTask.GetStatus()
	if info.Type != task.TaskTypeAgent {
		return continuedAgentTarget{}, fmt.Errorf("task %s is not an agent task", taskID)
	}
	if info.AgentSessionID == "" {
		return continuedAgentTarget{}, fmt.Errorf("task %s has no resumable agent_id yet", taskID)
	}

	target.agentID = info.AgentSessionID
	target.name = info.AgentName
	if target.agentType == "" {
		target.agentType = info.AgentType
	}
	if target.agentType == "" {
		target.agentType = "general-purpose"
	}
	return target, nil
}

func formatForegroundAgentResult(agentType string, result *tool.AgentExecResult, duration time.Duration) string {
	displayName := result.AgentName
	if displayName == "" {
		displayName = agentType
	}
	agentDuration := result.Duration
	if agentDuration == 0 {
		agentDuration = duration
	}

	var outputBuilder strings.Builder
	fmt.Fprintf(&outputBuilder, "Agent: %s\nModel: %s\nSteps: %d\nToolUses: %d\nTokens: %d\nDuration: %s\n",
		displayName, result.Model, result.StepCount, result.ToolUses, result.TotalTokens, toolresult.FormatDuration(agentDuration))
	if result.AgentID != "" {
		fmt.Fprintf(&outputBuilder, "AgentID: %s\n", result.AgentID)
	}
	if len(result.Progress) > 0 {
		fmt.Fprintf(&outputBuilder, "Process: %d\n", len(result.Progress))
	}
	outputBuilder.WriteString("\n")
	if len(result.Progress) > 0 {
		for _, p := range result.Progress {
			outputBuilder.WriteString(p)
			outputBuilder.WriteString("\n")
		}
	}
	if result.Content != "" {
		outputBuilder.WriteString(result.Content)
	}
	return outputBuilder.String()
}
