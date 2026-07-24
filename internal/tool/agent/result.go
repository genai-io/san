package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// maxResultActivityLines caps the tool activity echoed into the parent's
// context. The full trail stays visible in the TUI activity stream; the parent
// LLM only needs enough of the tail to sanity-check what the agent did.
const maxResultActivityLines = 30

// formatForegroundAgentResult renders a finished subagent's result for the
// parent's tool result: a short header, a capped tail of the tool trace, then
// the subagent's final message.
func formatForegroundAgentResult(agentName string, result *tool.AgentExecResult, duration time.Duration) string {
	displayName := result.AgentName
	if displayName == "" {
		displayName = agentName
	}
	agentDuration := result.Duration
	if agentDuration == 0 {
		agentDuration = duration
	}

	var outputBuilder strings.Builder
	fmt.Fprintf(&outputBuilder, "Agent: %s\nModel: %s\nSteps: %d\nToolUses: %d\nTokens: in=%d out=%d\nDuration: %s\n",
		displayName, result.Model, result.StepCount, result.ToolUses, result.TotalInputTokens, result.TotalOutputTokens, toolresult.FormatDuration(agentDuration))
	if result.AgentID != "" {
		fmt.Fprintf(&outputBuilder, "AgentID: %s\n", result.AgentID)
	}
	outputBuilder.WriteString("\n")

	activity := result.Activity
	if len(activity) > maxResultActivityLines {
		fmt.Fprintf(&outputBuilder, "(%d earlier tool calls omitted)\n", len(activity)-maxResultActivityLines)
		activity = activity[len(activity)-maxResultActivityLines:]
	}
	for _, line := range activity {
		outputBuilder.WriteString(line)
		outputBuilder.WriteString("\n")
	}
	if result.Content != "" {
		outputBuilder.WriteString(result.Content)
	}
	return outputBuilder.String()
}
