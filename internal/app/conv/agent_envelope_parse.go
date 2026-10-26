package conv

import (
	"html"
	"strings"
)

type agentEnvelopeKind string

const (
	envelopeTask    agentEnvelopeKind = "<task-notification"
	envelopeMessage agentEnvelopeKind = "<agent-message"
	envelopeBatch   agentEnvelopeKind = "<agent-messages"
	envelopeGroup   agentEnvelopeKind = "<group-message"
)

type agentEnvelope struct {
	kind                                                            agentEnvelopeKind
	content, tag, body                                              string
	attributed                                                      bool
	taskID, agentID, from, status, description, subject, outputFile string
}

func agentEnvelopeKindOf(content string) agentEnvelopeKind {
	for _, kind := range []agentEnvelopeKind{envelopeTask, envelopeBatch, envelopeMessage, envelopeGroup} {
		if strings.HasPrefix(content, string(kind)) {
			return kind
		}
	}
	return ""
}

func parseAgentEnvelope(content string) agentEnvelope {
	s := strings.TrimSpace(content)
	tag, body, _ := strings.Cut(s, ">")
	kind := agentEnvelopeKindOf(s)
	return agentEnvelope{
		kind: kind, content: s, tag: tag, body: body,
		attributed: kind != "" && strings.HasPrefix(tag, string(kind)+" "),
		taskID:     envelopeAttr(tag, "task-id"), agentID: envelopeAttr(tag, "agent-id"),
		from: envelopeAttr(tag, "from"), status: envelopeAttr(tag, "status"),
		description: envelopeAttr(tag, "description"), subject: envelopeAttr(tag, "subject"),
		outputFile: envelopeAttr(tag, "output-file"),
	}
}

func (e agentEnvelope) text() string {
	body := e.body
	if end := strings.LastIndex(body, "</"); end >= 0 {
		body = body[:end]
	}
	return html.UnescapeString(strings.TrimSpace(body))
}
