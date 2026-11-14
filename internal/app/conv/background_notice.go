package conv

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
)

// Identity colors link a launch to its return; only the status conveys severity.
func backgroundIdentityColor(id string) kit.AdaptiveColor {
	if id == "" {
		return kit.CurrentTheme.TextDim
	}
	return memberColor(id, nil)
}

func renderBackgroundShellCall(toolName, input, taskID string, width int) string {
	if width <= 0 {
		width = 80
	}
	command, description := extractBashCommand(input)
	label := toolName
	if description != "" {
		label += " · " + description
	}
	label = kit.TruncateText(label, max(1, width-kit.ToolIconWidth))
	header := renderAgentColoredLine(label, width, "●", backgroundIdentityColor(taskID))
	return header + "\n" + kit.RenderCommandBlock(command, width)
}

func backgroundStatusStyle(status string) lipgloss.Style {
	color := kit.CurrentTheme.Text
	switch status {
	case "completed", "succeeded":
		color = kit.CurrentTheme.Success
	case "failed":
		color = kit.CurrentTheme.Error
	case "interrupted", "timeout", "timed_out":
		color = kit.CurrentTheme.Warning
	}
	return lipgloss.NewStyle().Foreground(color)
}

func backgroundStatusLabel(status string) string {
	switch status {
	case "completed", "succeeded":
		return "Completed"
	case "failed":
		return "Failed"
	case "stopped", "killed":
		return "Stopped"
	case "interrupted":
		return "Interrupted"
	case "timeout", "timed_out":
		return "Timed out"
	case "":
		return "Updated"
	default:
		return status
	}
}

func (e agentEnvelope) renderTaskNotice(width int, name, description string, color kit.AdaptiveColor) string {
	if width <= 0 {
		width = 80
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Background task"
	}
	status := strings.Join(strings.Fields(e.status), " ")
	state := backgroundStatusStyle(e.status).Render(backgroundStatusLabel(status))
	suffix := toolResultStyle.Render(" · ") + state

	outcome := e
	outcome.description = ""
	outcome.status = status
	tail := outcome.summary(width)
	if reason, ok := strings.CutPrefix(tail, status); ok && status != "" && reason != "" {
		// On failures, keep identity and genuine diagnostics before task prose.
		room := max(0, width-kit.ToolIconWidth-ansi.StringWidth(suffix))
		nameBudget := min(ansi.StringWidth(name), room/2)
		suffix += toolCallStyle.Render(ansi.Truncate(reason, max(0, room-nameBudget), "…"))
	}

	identity := lipgloss.NewStyle().Foreground(color)
	budget := width - kit.ToolIconWidth - ansi.StringWidth(suffix)
	if budget <= 0 {
		line := identity.Render("● ") + state
		if ansi.StringWidth(line) > width {
			line = state
		}
		return ansi.Truncate(line, max(1, width), "…") + "\n"
	}
	label := identity.Render(ansi.Truncate(name, budget, "…"))
	description = strings.Join(strings.Fields(description), " ")
	if room := budget - ansi.StringWidth(name) - 2; description != "" && room > 0 {
		label += toolResultStyle.Render(": " + ansi.Truncate(description, room, "…"))
	}
	line := identity.Render("●") + " " + label + suffix
	return ansi.Truncate(line, max(1, width), "…") + "\n"
}
