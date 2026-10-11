package conv

import (
	"html"
	"strings"
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
func agentEnvelopeSummary(content string) string {
	s := strings.TrimSpace(content)
	tag, body, _ := strings.Cut(s, ">") // opening tag only, so the body can't spoof attributes
	switch {
	case strings.HasPrefix(s, "<group-message"):
		// The headers say who and what: "From @api: <subject>".
		head, _, _ := strings.Cut(s, "\n\n")
		var from, subject string
		for line := range strings.SplitSeq(head, "\n") {
			if v, ok := strings.CutPrefix(line, "From: "); ok {
				from = v
			} else if v, ok := strings.CutPrefix(line, "Subject: "); ok {
				subject = v
			}
		}
		switch {
		case from != "" && subject != "":
			return "From " + from + ": " + subject
		case from != "":
			return "From " + from
		}
	case strings.HasPrefix(s, "<task-notification"):
		status := envelopeAttr(tag, "status")
		if line := strings.TrimSpace(envelopeAttr(tag, "description") + " " + status); line != "" {
			if reason, ok := strings.CutPrefix(strings.TrimSpace(body), "Error: "); ok && (status == "failed" || status == "stopped" || status == "killed") {
				line += " · " + compactFailureReason(html.UnescapeString(reason), 80)
			}
			return strings.Join(strings.Fields(line), " ")
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
