package conv

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
)

// Background agents reach the main conversation as harness-injected envelopes: a
// <task-notification> when a background task finishes, an <agent-message> for an
// interim/relayed note, or an <agent-messages> batch of them. They are submitted
// to the model as user turns (so it can act on them) and persisted in that form.
// Live and resumed sessions render that same message as a one-line notice,
// leaving the model's Content untouched.

// IsAgentEnvelope reports whether a user message's content is one of those
// injected agent-comms envelopes rather than text the user typed.
func IsAgentEnvelope(content string) bool {
	s := strings.TrimSpace(content)
	// "<agent-message" also prefixes the "<agent-messages" batch wrapper.
	return strings.HasPrefix(s, "<task-notification") || strings.HasPrefix(s, "<agent-message") || strings.HasPrefix(s, "<group-message")
}

// agentEnvelopeSummary reduces an envelope to its one-line notice: the task's
// "<description> <status>", "Message from <sender>" for a relayed note, or a
// generic label when the identifying attributes are absent.
func agentEnvelopeSummary(content string, width int) string {
	if width <= 0 {
		width = 80
	}
	s := strings.TrimSpace(content)
	tag, body, _ := strings.Cut(s, ">") // opening tag only, so the body can't spoof attributes
	switch {
	case strings.HasPrefix(s, "<group-message"):
		// The headers say who and what: "From @api: <subject>".
		head, _, _ := strings.Cut(s, "\n\n")
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
			if count, _ := strconv.Atoi(envelopeAttr(tag, "count")); count > 1 {
				line += fmt.Sprintf(" (+%d more)", count-1)
			}
			if turns, _ := strconv.Atoi(unattended); turns >= 2 {
				line += fmt.Sprintf(" · unattended %d", turns)
			}
			return line
		}
	case strings.HasPrefix(s, "<task-notification"):
		status := strings.Join(strings.Fields(envelopeAttr(tag, "status")), " ")
		description := strings.Join(strings.Fields(envelopeAttr(tag, "description")), " ")
		if status != "" || description != "" {
			line := status
			if reason, ok := strings.CutPrefix(strings.TrimSpace(body), "Error: "); ok && (status == "failed" || status == "stopped" || status == "killed") {
				reason, _, _ = strings.Cut(html.UnescapeString(reason), "\n")
				line += " · " + strings.Join(strings.Fields(reason), " ")
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
	case strings.HasPrefix(s, "<agent-messages"):
		return "Messages from background agents"
	case strings.HasPrefix(s, "<agent-message"):
		if subject := envelopeAttr(tag, "subject"); subject != "" {
			return subject
		}
		if from := envelopeAttr(tag, "from"); from != "" {
			return "Message from " + from
		}
	}
	return "Background agent message"
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
