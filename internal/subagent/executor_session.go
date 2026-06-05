package subagent

import (
	"context"
	"fmt"

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

// resumeFromSession loads a previous subagent session and restores its conversation,
// then appends the new prompt as a continuation.
func (e *Executor) resumeFromSession(ag core.Agent, ctx context.Context, agentID, newPrompt string) error {
	if e.sessionStore == nil {
		return fmt.Errorf("session store not configured, cannot resume")
	}

	prevMessages, err := e.sessionStore.LoadSubagentMessages(agentID)
	if err != nil {
		return fmt.Errorf("load subagent messages for %s: %w", agentID, err)
	}

	ag.SetMessages(prevMessages)
	if newPrompt != "" {
		ag.Append(ctx, core.UserMessage(newPrompt, nil))
	}

	log.Logger().Info("Resumed agent from previous session",
		zap.String("agentID", agentID),
		zap.Int("previousMessages", len(prevMessages)),
	)
	return nil
}
