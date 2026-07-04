package conv

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

// maxAgentProgressLines is the maximum number of progress lines to display.
// Older lines scroll off the top, keeping the view compact.
const maxAgentProgressLines = 8

const (
	maxCompactAgentToolLines  = 3
	maxParallelAgentToolLines = 1
)

type AgentStats struct {
	Model        string
	InputTokens  int
	OutputTokens int
}

// renderAgentProgress renders the most recent agent progress lines,
// capped at maxAgentProgressLines to keep the view height bounded.
func renderAgentProgress(progress []string) string {
	if len(progress) == 0 {
		return ""
	}

	// Only show the most recent lines
	visible := progress
	if len(visible) > maxAgentProgressLines {
		visible = visible[len(visible)-maxAgentProgressLines:]
	}

	var sb strings.Builder
	for _, p := range visible {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  %s", p)) + "\n")
	}
	return sb.String()
}

func renderAgentProgressInline(tc core.ToolCall, pendingCalls []core.ToolCall, taskProgress map[int][]string, expanded bool, limit int, stats AgentStats) string {
	idx := -1
	for i, pending := range pendingCalls {
		if pending.ID == tc.ID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ""
	}

	progress := taskProgress[idx]
	if expanded {
		return renderAgentProgress(progress)
	}
	return renderAgentProgressCompact(tc.Input, progress, limit, stats)
}

func renderAgentProgressCompact(input string, progress []string, limit int, stats AgentStats) string {
	var sb strings.Builder
	if summary := agentSummary(input, progress, stats); summary != "" {
		sb.WriteString(toolResultStyle.Render("  ⎿  "+summary) + "\n")
	}

	toolLines := agentToolLines(progress, limit)
	for _, line := range toolLines {
		sb.WriteString(toolResultStyle.Render("  ⎿  "+line) + "\n")
	}
	if len(toolLines) == 0 {
		sb.WriteString(toolResultStyle.Render("  ⎿  "+agentStatus(progress)) + "\n")
	}
	return sb.String()
}

func agentSummary(input string, progress []string, stats AgentStats) string {
	parts := make([]string, 0, 4)
	if model := agentModel(progress, stats.Model); model != "" {
		parts = append(parts, "model: "+model)
	}
	if mode := agentMode(input, progress); mode != "" {
		parts = append(parts, "mode: "+mode)
	}
	if n := len(agentToolLines(progress, 0)); n > 0 {
		parts = append(parts, fmt.Sprintf("tools: %d", n))
	}
	if tokens := agentTokens(progress, stats); tokens != "" {
		parts = append(parts, "tokens: "+tokens)
	}
	return strings.Join(parts, "   ")
}

func agentModel(progress []string, fallback string) string {
	for i := len(progress) - 1; i >= 0; i-- {
		if model, ok := strings.CutPrefix(strings.TrimSpace(progress[i]), "Model: "); ok {
			return strings.TrimSpace(model)
		}
	}
	return fallback
}

func agentMode(input string, progress []string) string {
	if mode := parseAgentInput(input).Mode; mode != "" {
		return mode
	}
	for _, line := range progress {
		if after, ok := strings.CutPrefix(line, "Mode: "); ok {
			mode, _, _ := strings.Cut(after, " · ")
			return strings.TrimSpace(mode)
		}
	}
	return "default"
}

func tokenSummary(inputTokens, outputTokens int) string {
	if inputTokens <= 0 && outputTokens <= 0 {
		return ""
	}
	return fmt.Sprintf("↑%s ↓%s", kit.FormatTokenCount(inputTokens), kit.FormatTokenCount(outputTokens))
}

func agentTokens(progress []string, stats AgentStats) string {
	for i := len(progress) - 1; i >= 0; i-- {
		inputTokens, outputTokens, ok := parseUsageProgress(progress[i])
		if ok {
			return tokenSummary(inputTokens, outputTokens)
		}
	}
	return tokenSummary(stats.InputTokens, stats.OutputTokens)
}

func parseUsageProgress(line string) (int, int, bool) {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, "Usage: ")
	if !ok {
		return 0, 0, false
	}
	var inputTokens, outputTokens int
	for _, field := range strings.Fields(rest) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			continue
		}
		switch key {
		case "input":
			inputTokens = n
		case "output":
			outputTokens = n
		}
	}
	return inputTokens, outputTokens, inputTokens > 0 || outputTokens > 0
}

func agentStatus(progress []string) string {
	for i := len(progress) - 1; i >= 0; i-- {
		line := strings.TrimSpace(progress[i])
		if line == "" || isAgentToolLine(line) || strings.HasPrefix(line, "Mode: ") || strings.HasPrefix(line, "Model: ") || strings.HasPrefix(line, "Usage: ") {
			continue
		}
		return line
	}
	return "Starting..."
}

func agentToolLines(progress []string, limit int) []string {
	lines := make([]string, 0, len(progress))
	for _, line := range progress {
		if isAgentToolLine(line) {
			lines = append(lines, line)
		}
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

func isAgentToolLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "Mode: ") || strings.HasPrefix(line, "Model: ") || strings.HasPrefix(line, "Usage: ") || line == "Thinking..." {
		return false
	}
	return true
}

// PendingToolSpinnerParams holds the parameters for rendering a pending tool spinner.
type PendingToolSpinnerParams struct {
	// InteractivePromptActive indicates if an interactive prompt is currently active.
	InteractivePromptActive bool
	// BuildingTool is the tool name being built during streaming.
	BuildingTool string
	// PendingCalls are the pending tool calls.
	PendingCalls []core.ToolCall
	// CurrentIdx is the index of the current sequential tool.
	CurrentIdx int
	// TaskProgress tracks agent progress messages by index.
	TaskProgress map[int][]string
	// SpinnerView is the current spinner frame.
	SpinnerView string
	// Blink drives the agent running icon.
	Blink int
	// AgentColors maps agent type names to display colors.
	AgentColors map[string]string
	// Width is the terminal width for label truncation.
	Width int
	// SuppressAgentLabel avoids duplicating the active agent title when the
	// assistant message already rendered it above the progress lines.
	SuppressAgentLabel bool
}

// RenderPendingToolSpinner renders the spinner for a tool being executed.
func RenderPendingToolSpinner(params PendingToolSpinnerParams) string {
	if params.InteractivePromptActive {
		return ""
	}

	// Determine which tool is active
	var toolName string
	if params.BuildingTool != "" {
		toolName = params.BuildingTool
	} else if params.PendingCalls != nil && params.CurrentIdx < len(params.PendingCalls) {
		toolName = params.PendingCalls[params.CurrentIdx].Name
	} else {
		return ""
	}

	// Agent tool: render agent label + progress lines
	if tool.IsAgentToolName(toolName) {
		if params.SuppressAgentLabel {
			return ""
		}
		var sb strings.Builder
		// Show Agent label so it remains visible after the assistant message scrolls off.
		if params.PendingCalls != nil && params.CurrentIdx < len(params.PendingCalls) {
			tc := params.PendingCalls[params.CurrentIdx]
			label := formatAgentLabel(tc.Input)
			sb.WriteString(renderAgentToolLine(label, params.Width, agentIcon(params.Blink), agentColorForInput(tc.Input, params.AgentColors)) + "\n")
		}
		sb.WriteString(renderAgentProgress(params.TaskProgress[params.CurrentIdx]))
		return sb.String()
	}

	// Standard tools: spinner is shown inline in the assistant message row,
	// no separate spinner line needed.
	return ""
}
