package subagent

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
)

type continuationState struct {
	storeOwner                     *session.Store
	parentOwner                    *session.Session
	persistenceErr                 error
	originalPrompt                 string
	cfg                            *runConfig
	req                            tool.AgentExecRequest
	cwd, sessionID, parent, taskID string
}

func newContinuationState(run *preparedRun, parent, taskID string) *continuationState {
	state := &continuationState{originalPrompt: run.originalPrompt, cfg: run.cfg, req: run.req, cwd: run.cwd, parent: parent, taskID: taskID}
	state.req.OnIdentity = nil
	state.req.OnActivity, state.req.OnQuestion = nil, nil
	state.req.OnTextDelta, state.req.OnTextEnd = nil, nil
	state.req.OnToolStart, state.req.OnToolResult = nil, nil
	return state
}

// acceptContinuationInput binds the exact native prompt before async execution
// and before returning a positive SendMessage acknowledgement to its sender.
func (e *Executor) acceptContinuationInput(ctx context.Context, run *preparedRun) error {
	message := core.UserMessage(fmt.Sprintf("Continuation (previous execution: %s; current cwd: %s). Verify current filesystem/external state before retrying any interrupted tool with unknown outcome.\n", run.priorFailure, run.currentCwd())+run.req.Prompt, nil)
	message.ID = core.NewMessageID()
	if run.req.SourceSession != "" || run.req.SourceSeq != 0 {
		if run.req.SourceSession == "" || run.req.SourceSeq == 0 {
			return fmt.Errorf("incomplete continuation input source")
		}
		if err := run.recording.AcceptInput(ctx, message, &session.EventRef{Session: run.req.SourceSession, Seq: run.req.SourceSeq}); err != nil {
			return err
		}
	}
	run.continuationInput = &message
	return nil
}

func (e *Executor) openNative(ctx context.Context, run *preparedRun) error {
	if run.store == nil {
		return fmt.Errorf("agent execution requires session persistence")
	}
	if run.recording != nil {
		return nil
	}
	parentID := ""
	if run.parent != nil {
		if err := run.parent.Err(); err != nil {
			return err
		}
		parentID = run.parent.ID()
	}
	state := run.checkpoint
	if run.req.ResumeTaskID != "" {
		if state == nil || state.sessionID == "" || state.storeOwner != run.store || state.parentOwner != run.parent || state.parent != parentID || state.taskID != run.req.TaskID {
			return fmt.Errorf("missing, damaged or mismatched continuation identity")
		}
		if state.persistenceErr != nil {
			return fmt.Errorf("native history persistence failed; inspect/repair child transcript before continuing: %w", state.persistenceErr)
		}
		child, err := run.store.Open(ctx, state.sessionID)
		if err != nil {
			return err
		}
		snapshot := child.Snapshot()
		link := snapshot.Identity().Parent
		validLink := link == nil && parentID == ""
		if link != nil {
			validLink = link.Session == state.parent && link.CallID == state.req.ParentToolCallID
		}
		if !validLink || len(snapshot.ActiveMessages()) == 0 {
			_ = child.Close()
			return fmt.Errorf("child session identity or history mismatch")
		}
		run.recording, run.history = child, snapshot.ActiveMessages()
		return nil
	}
	var parent *session.ParentLink
	if parentID != "" {
		parent = &session.ParentLink{Session: parentID, CallID: run.req.ParentToolCallID}
	}
	provider, model, mode := run.cfg.provider.Name(), run.cfg.modelID, string(run.cfg.permMode)
	child, err := run.store.Create(ctx, session.CreateOptions{AgentID: run.req.TaskID, InitialCWD: run.currentCwd(), Parent: parent, Config: &session.EventConfig{Provider: &provider, Model: &model, Mode: &mode}})
	if err != nil {
		return err
	}
	run.recording = child
	if state != nil {
		state.sessionID, state.parentOwner, state.storeOwner = child.ID(), run.parent, run.store
	}
	return nil
}
