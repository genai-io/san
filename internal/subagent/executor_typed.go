package subagent

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/session"
)

func (e *Executor) openTyped(ctx context.Context, run *preparedRun) error {
	if e.sessionStore != nil || run.native != nil || run.typedParent == nil {
		return fmt.Errorf("mixed or missing child session owner")
	}
	if run.recording != nil {
		return nil
	}
	if err := run.typedParent.Err(); err != nil {
		return err
	}
	state := run.checkpoint
	if run.req.ResumeTaskID != "" {
		if state == nil || state.sessionID == "" || state.parentOwner != run.typedParent || state.parent != run.typedParent.ID() || state.taskID != run.req.TaskID || state.persistenceErr != nil {
			return fmt.Errorf("missing, damaged or mismatched typed continuation identity")
		}
		child, err := run.typedStore.Open(ctx, state.sessionID)
		if err != nil {
			return err
		}
		snapshot := child.Snapshot()
		link := snapshot.Identity().Parent
		if link == nil || link.Session != state.parent || link.CallID != state.req.ParentToolCallID || len(snapshot.ActiveMessages()) == 0 {
			_ = child.Close()
			return fmt.Errorf("child session identity or history mismatch")
		}
		run.recording, run.history = child, snapshot.ActiveMessages()
		return nil
	}
	if run.req.ParentToolCallID == "" {
		return fmt.Errorf("typed child requires originating tool call identity")
	}
	provider, model, mode := run.cfg.provider.Name(), run.cfg.modelID, string(run.cfg.permMode)
	child, err := run.typedStore.Create(ctx, session.CreateOptions{AgentID: run.req.TaskID, InitialCWD: run.currentCwd(), Parent: &session.ParentLink{Session: run.typedParent.ID(), CallID: run.req.ParentToolCallID}, Config: &session.EventConfig{Provider: &provider, Model: &model, Mode: &mode}})
	if err != nil {
		return err
	}
	run.recording = child
	if state != nil {
		state.sessionID, state.parentOwner = child.ID(), run.typedParent
	}
	return nil
}
