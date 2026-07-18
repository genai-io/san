package subagent

import (
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"go.uber.org/zap"
)

// persistSubagentSession saves the subagent conversation to disk if a session store is configured.
// Returns the session ID and transcript path (both empty if not persisted).
func (e *Executor) persistSubagentSession(agentName, modelID, description string, messages []core.Message) (string, string) {
	if e.sessionStore == nil || e.parentSessionID == "" {
		return "", ""
	}

	title := description
	if title == "" {
		title = agentName
	}
	sessionID, transcriptPath, err := e.sessionStore.SaveSubagentConversation(e.parentSessionID, title, modelID, e.cwd, messages)
	if err != nil {
		log.Logger().Warn("Failed to persist subagent session",
			zap.String("agent", agentName),
			zap.Error(err),
		)
		return "", ""
	}
	return sessionID, transcriptPath
}
