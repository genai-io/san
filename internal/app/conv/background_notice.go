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

func (e agentEnvelope) renderTaskNotice(width int, name string, color kit.AdaptiveColor) string {
	if width <= 0 {
		width = 80
	}
	if name == "" {
		name = "Background task"
	}
	status := strings.Join(strings.Fields(e.status), " ")
	if status == "" {
		status = "updated"
	}
	name = strings.Join(strings.Fields(name), " ")
	nameBudget := min(max(0, width-ansi.StringWidth(status)-5), max(0, width/2-2))
	title := ""
	if nameBudget > 0 {
		title = kit.TruncateText(name, nameBudget)
	}
	identity := lipgloss.NewStyle().Foreground(color)
	line := identity.Render("●") + " "
	if title != "" {
		line += identity.Render(title) + " "
	}
	outcome := e
	outcome.description = ""
	outcome.status = status
	tail := outcome.summary(max(3, width-ansi.StringWidth(line)+2))
	reason := strings.TrimPrefix(tail, status)
	if !strings.HasPrefix(tail, status) {
		status, reason = tail, ""
	}
	line += backgroundStatusStyle(e.status).Render(status) + toolCallStyle.Render(reason)
	if e.taskID != "" {
		id := " · " + e.taskID
		if ansi.StringWidth(line)+ansi.StringWidth(id) <= width {
			line += toolResultStyle.Render(id)
		}
	}
	return kit.TruncateText(line, max(1, width)) + "\n"
}
