package conv

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
)

// Interim notes and ordinary background completions arrive as model-visible user
// envelopes and render as one-line notices. Display-only task annotations reuse
// the same identity heading with a bounded worker-answer preview.

// IsAgentEnvelope reports whether a user message's content is one of those
// injected agent-comms envelopes rather than text the user typed.
func IsAgentEnvelope(content string) bool {
	return agentEnvelopeKindOf(strings.TrimSpace(content)) != ""
}

// agentEnvelopeSummary reduces an envelope to its one-line notice: the task's
// "<description> <status>", "Message from <sender>" for a relayed note, or a
// generic label when the identifying attributes are absent.
func agentEnvelopeSummary(content string, width int) string {
	return parseAgentEnvelope(content).summary(width)
}

func (e agentEnvelope) summary(width int) string {
	if width <= 0 {
		width = 80
	}
	switch e.kind {
	case envelopeGroup:
		// The headers say who and what: "From @api: <subject>".
		head, _, _ := strings.Cut(e.content, "\n\n")
		var from, subject, unattended string
		for line := range strings.SplitSeq(head, "\n") {
			if v, ok := strings.CutPrefix(line, "From: "); ok {
				from = v
			} else if v, ok := strings.CutPrefix(line, "Subject: "); ok {
				subject = v
			} else if v, ok := strings.CutPrefix(line, "Unattended-Turns: "); ok {
				unattended = v
			}
		}
		if from != "" {
			line := "From " + from
			if subject != "" {
				line += ": " + subject
			}
			var notes []string
			if count, _ := strconv.Atoi(envelopeAttr(e.tag, "count")); count > 1 {
				notes = append(notes, fmt.Sprintf("(+%d more)", count-1))
			}
			if turns, _ := strconv.Atoi(unattended); turns >= 2 {
				notes = append(notes, fmt.Sprintf("unattended %d", turns))
			}
			if len(notes) > 0 {
				line += " · " + strings.Join(notes, ", ")
			}
			return line
		}
	case envelopeTask:
		status := strings.Join(strings.Fields(e.status), " ")
		description := strings.Join(strings.Fields(e.description), " ")
		if status != "" || description != "" {
			line := status
			if reason, ok := strings.CutPrefix(e.text(), "Error: "); ok && (status == "failed" || status == "stopped" || status == "killed" || status == "interrupted" || status == "timeout" || status == "timed_out") {
				reason, _, _ = strings.Cut(reason, "\n")
				if status == "failed" || !kit.IsRoutineStopReason(reason) {
					line += " · " + strings.Join(strings.Fields(reason), " ")
				}
			}
			line = kit.TruncateText(line, max(1, width-2))
			// Reserve the outcome before shortening a task's description.
			if budget := width - 3 - ansi.StringWidth(line); budget > 0 {
				description = kit.TruncateText(description, budget)
			} else {
				description = ""
			}
			return strings.TrimSpace(description + " " + line)
		}
	case envelopeBatch:
		return "Messages from background agents"
	case envelopeMessage:
		if e.subject != "" {
			return e.subject
		}
		if e.from != "" {
			return "Message from " + e.from
		}
	}
	return "Background agent message"
}

// Display-only worker answers use the same Agent block as their live activity.
func (e agentEnvelope) renderDisplayNotice(p RenderContext) string {
	if e.isScheduler() {
		return e.renderFlowNotice(p)
	}
	v := AgentView{
		ID: e.taskID, SessionID: e.agentID, Name: envelopeAttr(e.tag, "agent-name"),
		Description: e.description, Status: e.status, Output: e.text(),
	}
	if milliseconds, err := strconv.ParseInt(envelopeAttr(e.tag, "duration-ms"), 10, 64); err == nil && milliseconds > 0 {
		v.Duration = time.Duration(milliseconds) * time.Millisecond
	}
	switch e.status {
	case "failed", "stopped", "killed", "interrupted", "timeout", "timed_out":
		if reason, ok := strings.CutPrefix(v.Output, "Error: "); ok {
			v.Error, v.Output, _ = strings.Cut(reason, "\n")
		}
		v.Output = strings.TrimSpace(v.Output)
		v.Output = strings.TrimSpace(strings.TrimPrefix(v.Output, "Partial output:"))
		if kit.IsRoutineStopReason(v.Output) {
			v.Output = ""
		}
	}
	return renderAgentView(v, p)
}

func (e agentEnvelope) renderWithIdentity(p RenderContext) string {
	if e.isScheduler() {
		return e.renderFlowNotice(p)
	}
	if p.Width <= 0 {
		p.Width = 80
	}
	if e.kind == envelopeMessage && e.attributed && e.from != "" {
		body := strings.Join(strings.Fields(e.text()), " ")
		return renderAgentMessageLine("●", "From", e.from, body, p.Width, p.AgentIdentities, p.AgentColors, p.MemberColors) + "\n"
	}
	if e.kind == envelopeTask && e.attributed {
		identity, found := p.AgentIdentities[e.taskID]
		if !found {
			identity, found = p.AgentIdentities[e.agentID]
		}
		if found {
			name := AgentDisplayName(identity, p.AgentIdentities)
			description := agentDescription(identity.Name, e.description)
			return e.renderTaskNotice(p.Width, name, description, agentIdentityColor(identity, p.AgentColors))
		}
	}
	return e.render(p.Width, p.MemberColors)
}

// renderAgentEnvelope derives severity from structured terminal status, never
// from a word such as "failed" in an agent's prose or task description.
func (e agentEnvelope) render(width int, colors map[string]int) string {
	summary := e.summary(width)
	if e.kind == envelopeTask && e.attributed {
		return e.renderTaskNotice(width, e.description, "", backgroundIdentityColor(e.taskID))
	}
	return RenderAgentNotice(summary, width, colors)
}

func (e agentEnvelope) isScheduler() bool {
	return e.kind == envelopeTask && e.attributed && envelopeAttr(e.tag, "scheduler") == "true"
}

func (e agentEnvelope) renderFlowNotice(p RenderContext) string {
	width := p.Width
	if width <= 0 {
		width = 80
	}
	body := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text)
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	status := backgroundStatusLabel(e.status)
	if e.status == "running" {
		status = "Started"
	}
	statusStyle := backgroundStatusStyle(e.status).Bold(false)
	if status == "Stopped" {
		statusStyle = muted
	}
	outcome := statusStyle.Render(status)
	// Reserve the outcome before optional title and elapsed detail.
	if width < ansi.StringWidth("● Flow · ")+ansi.StringWidth(outcome) {
		return ansi.Truncate(outcome, width, "…") + "\n" + e.renderFlowResult(p)
	}
	elapsed := ""
	if milliseconds, err := strconv.ParseInt(envelopeAttr(e.tag, "duration-ms"), 10, 64); err == nil {
		elapsed = muted.Render(" · " + (time.Duration(max(int64(0), milliseconds)) * time.Millisecond).Truncate(time.Second).String())
	}
	detail := body.Render(" · ") + outcome
	baseWidth := ansi.StringWidth("● Flow")
	// Reserve genuine diagnostics before optional title and elapsed detail.
	diagnostic := ""
	if reason, ok := strings.CutPrefix(strings.TrimSpace(e.text()), "Error: "); ok && e.status != "completed" && e.status != "running" {
		reason, _, _ = strings.Cut(reason, "\n")
		if e.status == "failed" || !kit.IsRoutineStopReason(reason) {
			if budget := width - baseWidth - ansi.StringWidth(detail) - 3; budget > 0 {
				diagnostic = body.Render(" · " + ansi.Truncate(reason, budget, "…"))
			}
		}
	}
	if baseWidth+ansi.StringWidth(detail)+ansi.StringWidth(diagnostic)+ansi.StringWidth(elapsed) > width {
		elapsed = ""
	}
	detail += elapsed + diagnostic
	title := strings.Join(strings.Fields(envelopeAttr(e.tag, "agent-name")), " ")
	label := body.Render("● ") + kit.FlowLabel(title)
	label = ansi.Truncate(label, width-ansi.StringWidth(detail), "…")
	return label + detail + "\n" + e.renderFlowResult(p)
}

func (e agentEnvelope) renderFlowResult(p RenderContext) string {
	if e.status != "completed" || envelopeAttr(e.tag, "result-displayed") == "true" {
		return ""
	}
	width := p.Width
	if width <= 0 {
		width = 80
	}
	result := e.text()
	if strings.TrimSpace(result) == "" {
		return "\n" + toolResultStyle.Render(ansi.Truncate("No final result", width, "…")) + "\n"
	}
	md := p.MDRenderer
	if md == nil {
		md = NewMDRenderer(width)
	}
	rendered := ansi.Hardwrap(renderMarkdownContent(md, result), width, true)
	// Older persisted notices may contain only an excerpt.
	if envelopeAttr(e.tag, "result-truncated") == "true" {
		hint := "Recorded excerpt; full result in task details"
		rendered += "\n\n" + toolResultStyle.Render(ansi.Hardwrap(hint, width, true))
	}
	return "\n" + strings.TrimRight(rendered, "\n") + "\n"
}

// envelopeAttr returns the value of a name="value" attribute within an opening
// tag, or "" when absent.
func envelopeAttr(tag, name string) string {
	// Attributes are space-separated and values are escaped, so a leading space
	// keeps "agent-id" from matching inside another attribute's name.
	_, after, found := strings.Cut(tag, " "+name+`="`)
	if !found {
		return ""
	}
	value, _, closed := strings.Cut(after, `"`)
	if !closed {
		return ""
	}
	return html.UnescapeString(value)
}
