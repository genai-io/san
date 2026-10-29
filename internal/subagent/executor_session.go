package subagent

import (
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"go.uber.org/zap"
)

type liveSessionStore interface {
	Create(session.CreateOptions) (*session.Session, error)
	Open(string) (*session.Session, *session.History, error)
}

type continuationState struct {
	persistenceErr                 error
	originalPrompt                 string
	cfg                            *runConfig
	req                            tool.AgentExecRequest
	cwd, sessionID, parent, taskID string
}

func newContinuationState(run *preparedRun, parent, taskID string) *continuationState {
	state := &continuationState{originalPrompt: run.originalPrompt, cfg: run.cfg, req: run.req, cwd: run.cwd, parent: parent, taskID: taskID}
	// Do not retain the completed invocation's UI callbacks.
	state.req.OnIdentity = nil
	state.req.OnActivity, state.req.OnQuestion = nil, nil
	state.req.OnTextDelta, state.req.OnTextEnd = nil, nil
	state.req.OnToolStart, state.req.OnToolResult = nil, nil
	return state
}

func (e *Executor) openNative(run *preparedRun) error {
	if run.native != nil {
		return nil // A continuation claims its writer before beginning an attempt.
	}
	store, ok := e.sessionStore.(liveSessionStore)
	if !ok {
		if run.req.ResumeTaskID != "" {
			return fmt.Errorf("native continuation requires live session persistence")
		}
		return nil
	}
	if e.parentSessionID == "" {
		return fmt.Errorf("native child session requires parent identity")
	}
	var err error
	if run.req.ResumeTaskID != "" {
		state := run.checkpoint
		if state == nil || state.sessionID == "" || state.parent != e.parentSessionID || state.taskID != run.req.TaskID {
			return fmt.Errorf("missing or mismatched continuation identity")
		}
		if state.persistenceErr != nil {
			return fmt.Errorf("native history persistence failed; inspect/repair child transcript before continuing: %w", state.persistenceErr)
		}
		var history *session.History
		run.native, history, err = store.Open(state.sessionID)
		if err != nil {
			return err
		}
		if history == nil || len(history.Messages) == 0 {
			_ = run.native.Close()
			return fmt.Errorf("native history is empty or damaged for child session %s; inspect its transcript before continuing", state.sessionID)
		}
		if history.Metadata.ParentSessionID != e.parentSessionID || history.Metadata.ID != state.sessionID {
			_ = run.native.Close()
			return fmt.Errorf("child session identity mismatch")
		}
		run.history = history.Messages
	} else {
		run.native, err = store.Create(session.CreateOptions{AgentID: run.req.TaskID, ParentSessionID: e.parentSessionID, Model: run.cfg.modelID, Cwd: run.currentCwd()})
		if err == nil && run.checkpoint != nil {
			run.checkpoint.sessionID = run.native.ID()
		}
	}
	return err
}

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
	sessionID, transcriptPath, err := e.sessionStore.RecordSubagentConversation(e.parentSessionID, title, modelID, e.cwd, messages)
	if err != nil {
		log.Logger().Warn("Failed to persist subagent session",
			zap.String("agent", agentName),
			zap.Error(err),
		)
		return "", ""
	}
	return sessionID, transcriptPath
}
