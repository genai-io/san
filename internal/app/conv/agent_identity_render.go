package conv

import (
	"strings"

	"github.com/genai-io/san/internal/app/kit"
)

func shortAgentDisplayID(id string) string {
	if len(id) > 12 {
		return id[:4] + "…" + id[len(id)-4:]
	}
	return id
}

func agentIdentityColor(identity AgentDisplay, configured map[string]string) kit.AdaptiveColor {
	if color := configured[strings.ToLower(identity.Name)]; color != "" {
		return agentColor(color)
	}
	return memberPalette[max(0, identity.ColorKey)%len(memberPalette)]
}

func renderAgentMessageLine(icon, dir, id, body string, width int, identities map[string]AgentDisplay, configured map[string]string, members map[string]int) string {
	if identity, ok := identities[id]; ok {
		return renderIdentityMessageLine(icon, dir, AgentDisplayName(identity, identities), body, width, agentIdentityColor(identity, configured), toolResultStyle)
	}
	if _, member := members[id]; member || id == "main" {
		return renderMemberLine(icon, dir, id, body, width, members)
	}
	return renderIdentityMessageLine(icon, dir, shortAgentDisplayID(id), body, width, memberColor(id, members), toolResultStyle)
}
