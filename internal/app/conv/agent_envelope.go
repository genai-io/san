package conv

import "strings"

// Background agents reach the main conversation as harness-injected envelopes: a
// <task-notification> when a background task finishes, an <agent-message> for an
// interim/relayed note, or an <agent-messages> batch of them. They are submitted
// to the model as user turns (so it can act on them) and persisted in that form.
// A live session shows a friendly one-line notice instead, but that notice is
// UI-only; on resume the conversation is rebuilt from the transcript and these
// turns would otherwise render as a raw "❭ <task-notification …>" XML dump.
// isAgentEnvelope + agentEnvelopeSummary let the renderer collapse them back to
// the same one-line notice while leaving the model's Content untouched.

// isAgentEnvelope reports whether a user message's content is one of those
// injected agent-comms envelopes rather than text the user typed.
func isAgentEnvelope(content string) bool {
	s := strings.TrimSpace(content)
	// "<agent-message" also prefixes the "<agent-messages" batch wrapper.
	return strings.HasPrefix(s, "<task-notification") || strings.HasPrefix(s, "<agent-message")
}

// agentEnvelopeSummary reduces an envelope to its one-line notice: the task's
// "<description> <status>", "Message from <sender>" for a relayed note, or a
// generic label when the identifying attributes are absent.
func agentEnvelopeSummary(content string) string {
	s := strings.TrimSpace(content)
	tag, _, _ := strings.Cut(s, ">") // opening tag only, so the body can't spoof attributes
	switch {
	case strings.HasPrefix(s, "<task-notification"):
		if line := strings.TrimSpace(envelopeAttr(tag, "description") + " " + envelopeAttr(tag, "status")); line != "" {
			return line
		}
	case strings.HasPrefix(s, "<agent-messages"):
		return "Messages from background agents"
	case strings.HasPrefix(s, "<agent-message"):
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
	return value
}
