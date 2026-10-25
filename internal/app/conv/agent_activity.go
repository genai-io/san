package conv

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

// maxAgentActivityLines is the maximum number of activity lines to display.
// Older lines scroll off the top, keeping the view compact.
const maxAgentActivityLines = 8

// renderAgentActivity renders the most recent agent activity lines,
// capped at maxAgentActivityLines to keep the view height bounded.
func renderAgentActivity(activity []string) string {
	if len(activity) == 0 {
		return ""
	}

	// Only show the most recent lines
	visible := activity
	if len(visible) > maxAgentActivityLines {
		visible = visible[len(visible)-maxAgentActivityLines:]
	}

	var sb strings.Builder
	sb.WriteString(toolResultStyle.Render("  ⎿  Recent activity") + "\n")
	if omitted := len(activity) - len(visible); omitted > 0 {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  … %d retained entries omitted", omitted)) + "\n")
	}
	for _, p := range visible {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  %s", p)) + "\n")
	}
	return sb.String()
}

func renderAgentActivityInline(tc core.ToolCall, pendingCalls []core.ToolCall, taskActivity map[int][]string, expanded bool, width int) string {
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

	activity := taskActivity[idx]
	if expanded {
		return renderAgentActivity(activity)
	}
	return renderAgentActivityCompact(activity, width)
}

func renderAgentActivityCompact(activity []string, width int) string {
	if width <= 0 {
		width = 80
	}
	return renderAgentLocalBody(compactFailureReason(agentStatus(activity), width), width)
}

// activityMetadataKey identifies replaceable status lines in the existing
// worker activity protocol; these do not consume the recent-tool budget.
func activityMetadataKey(line string) string {
	for _, prefix := range []string{"Model: ", "Mode: ", "Usage: ", "Omitted: "} {
		if strings.HasPrefix(line, prefix) {
			return prefix
		}
	}
	return ""
}

func retainAgentActivity(activity []string, message string) []string {
	return retainAgentActivityLimit(activity, message, maxAgentActivityHistory)
}

func retainAgentActivityLimit(activity []string, message string, limit int) []string {
	var metadata, recent []string
	for _, line := range append(activity, message) {
		key := activityMetadataKey(line)
		if key == "" {
			recent = append(recent, line)
			continue
		}
		metadata = slices.DeleteFunc(metadata, func(old string) bool { return strings.HasPrefix(old, key) })
		metadata = append(metadata, line)
	}
	if len(recent) > limit {
		recent = recent[len(recent)-limit:]
	}
	return append(metadata, recent...)
}

func agentStatus(activity []string) string {
	for _, a := range slices.Backward(activity) {
		line := strings.TrimSpace(a)
		if line == "" || activityMetadataKey(line) != "" {
			continue
		}
		// Shell progress can share the worker feed. A bare output count is
		// bookkeeping, not a useful description of the Agent's current work.
		head, _, _ := strings.Cut(line, "\n")
		if fields := strings.Fields(head); len(fields) == 2 && (fields[1] == "line" || fields[1] == "lines") {
			if _, err := strconv.ParseFloat(strings.TrimRight(fields[0], "kKmM"), 64); err == nil {
				return "Working..."
			}
		}
		return line
	}
	return "Starting..."
}

// PendingToolSpinnerParams holds the parameters for rendering a pending tool spinner.
type PendingToolSpinnerParams struct {
	// DockedModalActive indicates a Question / Approval / secret modal owns
	// the screen, so the turn is parked on the user's answer.
	DockedModalActive bool
	// BuildingTool is the tool name being built during streaming.
	BuildingTool string
	// PendingCalls are the pending tool calls.
	PendingCalls []core.ToolCall
	// CurrentIdx is the index of the current sequential tool.
	CurrentIdx int
	// ToolStartedAt is execution evidence, not merely a streamed tool announcement.
	ToolStartedAt map[string]time.Time
	// TaskActivity tracks agent activity messages by index.
	TaskActivity map[int][]string
	// SpinnerView is the current spinner frame.
	SpinnerView string
	// Frame drives the agent running icon.
	Frame int
	// AgentColors maps agent names to display colors.
	AgentColors map[string]string
	// Width is the terminal width for label truncation.
	Width int
	// SuppressAgentLabel avoids duplicating the active agent title when the
	// assistant message already rendered it above the activity lines.
	SuppressAgentLabel bool
}

// RenderPendingToolSpinner renders the spinner for a tool being executed.
func RenderPendingToolSpinner(params PendingToolSpinnerParams) string {
	if params.DockedModalActive {
		return ""
	}

	// Determine which tool is active
	var toolName string
	if params.BuildingTool != "" {
		toolName = params.BuildingTool
	} else if params.CurrentIdx >= 0 && params.CurrentIdx < len(params.PendingCalls) {
		toolName = params.PendingCalls[params.CurrentIdx].Name
	} else {
		return ""
	}

	// Use the same Agent row as the conversation. A separate fallback state
	// machine can revive stale activity or call a started worker "Pending".
	if tool.IsAgentToolName(toolName) {
		if params.SuppressAgentLabel {
			return ""
		}
		if params.CurrentIdx >= 0 && params.CurrentIdx < len(params.PendingCalls) {
			tc := params.PendingCalls[params.CurrentIdx]
			if tool.IsAgentToolName(tc.Name) {
				return RenderToolCalls(ToolCallsParams{
					ToolCalls: []core.ToolCall{tc}, PendingCalls: params.PendingCalls,
					CurrentIdx: params.CurrentIdx, ToolStartedAt: params.ToolStartedAt,
					TaskActivity: params.TaskActivity, Frame: params.Frame,
					AgentColors: params.AgentColors, Width: params.Width,
				})
			}
		}
		// The tool name may arrive before its arguments finish streaming.
		return renderAgentToolLine(toolName, params.Width, "●", "") + "\n" +
			renderAgentLocalBody("Pending", params.Width)
	}

	// Standard tools: spinner is shown inline in the assistant message row,
	// no separate spinner line needed.
	return ""
}
