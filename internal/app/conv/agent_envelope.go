package conv

import (
	"fmt"
	"html"
	"strconv"
	"strings"

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

// renderDisplayNotice adds worker prose only for display-only task annotations.
// The heading already carries the outcome, including genuine failure diagnostics.
func (e agentEnvelope) renderDisplayNotice(p RenderContext) string {
	heading := e.renderWithIdentity(p)
	if p.AgentPreviewLines <= 0 {
		return heading
	}
	body := e.text()
	switch e.status {
	case "failed", "stopped", "killed", "interrupted", "timeout", "timed_out":
		if strings.HasPrefix(body, "Error: ") {
			_, body, _ = strings.Cut(body, "\n")
		}
		if kit.IsRoutineStopReason(strings.TrimSpace(body)) {
			body = ""
		}
	}
	if strings.TrimSpace(body) == "" {
		return heading
	}
	return heading + renderAgentResponsePreview(body, p.Width, p.MDRenderer, p.AgentPreviewLines)
}

func (e agentEnvelope) renderWithIdentity(p RenderContext) string {
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

// envelopeAttr returns the value of a name="value" attribute within an opening
// tag, or "" when absent.
func envelopeAttr(tag, name string) string {
	_, after, found := strings.Cut(tag, name+`="`)
	if !found {
		return ""
	}
	value, _, closed := strings.Cut(after, `"`)
	if !closed {
		return ""
	}
	return html.UnescapeString(value)
}
