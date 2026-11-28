package chat

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/genai-io/san/internal/app/tui/widgets"
)

// AgentView projects a real worker into the native Agent presentation, without a tool call.
type AgentView struct {
	ID, SessionID, Name, Description string
	StartedAt                        time.Time
	Duration                         time.Duration
	Activity                         []string
	Status, Output, Error            string
	AwaitingApproval                 bool
}

func renderAgentView(v AgentView, p RenderContext) string {
	if p.Width <= 0 {
		p.Width = 80
	}
	name := v.Name
	if name == "" {
		name = "Agent"
	}
	color := backgroundIdentityColor(v.ID)
	if configured := p.AgentColors[strings.ToLower(name)]; configured != "" {
		color = agentColor(configured)
	}
	identity, found := p.AgentIdentities[v.ID]
	if !found {
		identity, found = p.AgentIdentities[v.SessionID]
	}
	description := agentDescription(name, v.Description)
	if found {
		description = agentDescription(identity.Name, v.Description)
		name = AgentDisplayName(identity, p.AgentIdentities)
		color = agentIdentityColor(identity, p.AgentColors)
	}
	label := formatAgentLabel(agentInput{Name: name, Description: description, Valid: true})
	running := v.Status == "running"
	detail, note := "", ""
	if running && v.AwaitingApproval {
		detail = toolResultStyle.Render(" · awaiting approval")
	} else if running && !p.DockedModalActive && !v.StartedAt.IsZero() {
		detail = agentElapsed(time.Since(v.StartedAt))
	} else if !running {
		status := backgroundStatusLabel(v.Status)
		if v.Status == "completed" {
			status = "Finished"
		}
		reason := v.Error
		if v.Status != "failed" && widgets.IsRoutineStopReason(reason) {
			reason = ""
		}
		style := backgroundStatusStyle(v.Status)
		if v.Status == "completed" {
			style = toolResultStyle
		}
		detail, note = toolStatusDetail(name, status, compactFailureReason(reason, p.Width), style, p.Width)
	}
	if running && lipgloss.Width(detail) > max(0, p.Width-lipgloss.Width("● Agent")) {
		detail = ""
	}
	width := toolHeadingWidth(p.Width, detail)
	label = widgets.TruncateText(label, max(1, width-widgets.ToolIconWidth))
	icon := "●"
	if running && !v.AwaitingApproval && !p.DockedModalActive {
		icon = lipgloss.NewStyle().Foreground(color).Faint(p.Frame%6 < 2).Render(icon)
	}
	heading := appendRowDetail(renderAgentHeading(label, width, icon, color)+"\n", detail)
	if running {
		if v.AwaitingApproval {
			return heading + renderAgentLocalBody("Waiting for approval", p.Width)
		}
		return heading + renderAgentActivityPreview(v.Activity, p.Width, p.AgentPreviewLines)
	}
	body := renderAgentLocalBody(note, p.Width)
	if detail == "" && v.Status != "completed" {
		status := backgroundStatusLabel(v.Status)
		if v.Error != "" && (v.Status == "failed" || !widgets.IsRoutineStopReason(v.Error)) {
			status += " · " + v.Error
		}
		body = backgroundStatusStyle(v.Status).Render("  "+compactFailureReason(status, p.Width)) + "\n"
	}
	if p.AgentPreviewLines > 0 && strings.TrimSpace(v.Output) != "" {
		body += renderAgentResponsePreview(v.Output, p.Width, p.MDRenderer, p.AgentPreviewLines)
	}
	return heading + body
}

func agentElapsed(duration time.Duration) string {
	seconds := max(0, int(duration/time.Second))
	elapsed := fmt.Sprintf("%ds", seconds)
	if seconds >= 60 {
		elapsed = fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return toolResultStyle.Render(" · " + elapsed)
}
