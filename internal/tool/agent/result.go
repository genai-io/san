package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// maxResultTraceLines caps the tool trace echoed into the parent's context.
// The full trace stays visible in the TUI progress stream; the parent LLM
// only needs enough of the tail to sanity-check what the agent did.
const maxResultTraceLines = 30

// formatForegroundAgentResult renders a finished subagent's result for the
// parent's tool result: a short header, a capped tail of the tool trace, then
// the subagent's final message.
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
	fmt.Fprintf(&outputBuilder, "Agent: %s\nModel: %s\nSteps: %d\nToolUses: %d\nTokens: in=%d out=%d\nDuration: %s\n",
		displayName, result.Model, result.StepCount, result.ToolUses, result.TotalInputTokens, result.TotalOutputTokens, toolresult.FormatDuration(agentDuration))
	if result.AgentID != "" {
		fmt.Fprintf(&outputBuilder, "AgentID: %s\n", result.AgentID)
	}
	outputBuilder.WriteString("\n")

	trace := result.Activity
	if len(trace) > maxResultTraceLines {
		fmt.Fprintf(&outputBuilder, "(%d earlier tool calls omitted)\n", len(trace)-maxResultTraceLines)
		trace = trace[len(trace)-maxResultTraceLines:]
	}
	for _, p := range trace {
		outputBuilder.WriteString(p)
		outputBuilder.WriteString("\n")
	}
	if result.Content != "" {
		outputBuilder.WriteString(result.Content)
	}
	return outputBuilder.String()
}
