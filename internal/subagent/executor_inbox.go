package subagent

import (
	"context"
	"fmt"
	"sync"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

const maxPendingWorkerMessages = 64

// runInbox owns acceptance from launch preparation through execution shutdown.
type runInbox struct {
	mu         sync.Mutex
	ctx        context.Context
	owner      *session.Session
	agent      core.Agent
	pending    []core.Message
	accepting  bool
	closed     bool
	unregister func()
	stop       func() bool
}

func newRunInbox(ctx context.Context, taskID string, owner *session.Session) *runInbox {
	inbox := &runInbox{ctx: ctx, owner: owner, accepting: true}
	if taskID != "" {
		inbox.unregister = broker.Register(taskID, inbox.deliver)
	}
	inbox.mu.Lock()
	inbox.stop = context.AfterFunc(ctx, inbox.close)
	inbox.mu.Unlock()
	return inbox
}

func (inbox *runInbox) deliver(m broker.Message) bool {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if !inbox.accepting || inbox.closed || inbox.ctx.Err() != nil || (inbox.owner != nil && inbox.owner.Err() != nil) {
		return false
	}
	pending := len(inbox.pending)
	if inbox.agent != nil {
		pending += inbox.agent.Pending()
	}
	if pending >= maxPendingWorkerMessages {
		return false
	}
	message := core.UserMessage(m.Content, nil)
	var source *session.EventRef
	if m.SourceSession != "" || m.SourceSeq != 0 {
		if inbox.owner == nil || m.SourceSession == "" || m.SourceSeq == 0 {
			return false
		}
		source = &session.EventRef{Session: m.SourceSession, Seq: m.SourceSeq}
	}
	if inbox.owner != nil {
		message.ID = core.NewMessageID()
		if err := inbox.owner.AcceptInput(inbox.ctx, message, source); err != nil {
			return false
		}
	}
	if inbox.agent == nil {
		inbox.pending = append(inbox.pending, message)
	} else {
		inbox.agent.Append(message)
	}
	return true
}

func (inbox *runInbox) close() {
	inbox.mu.Lock()
	if inbox.closed {
		inbox.mu.Unlock()
		return
	}
	inbox.closed = true
	inbox.pending, inbox.agent, inbox.owner = nil, nil, nil
	stop, unregister := inbox.stop, inbox.unregister
	inbox.stop, inbox.unregister = nil, nil
	inbox.mu.Unlock()
	if stop != nil {
		stop()
	}
	if unregister != nil {
		unregister()
	}
}

// prepareInbox commits launch input before publishing a route that accepts mail.
func (e *Executor) prepareInbox(ctx context.Context, run *preparedRun) error {
	if e.tasks != nil {
		return nil // Session-owned hosts do not publish TUI broker addresses.
	}
	if err := e.openNative(ctx, run); err != nil {
		return err
	}
	input := run.continuationInput
	if input == nil {
		message := core.UserMessage(run.originalPrompt, nil)
		message.ID = core.NewMessageID()
		input = &message
	}
	// Sourced continuations were already committed during their permission gate.
	if run.req.ResumeTaskID == "" || (run.req.SourceSession == "" && run.req.SourceSeq == 0) {
		if err := run.recording.AcceptInput(ctx, *input, nil); err != nil {
			_ = run.recording.Close()
			return err
		}
	}
	run.initialInput = input
	run.inbox = newRunInbox(ctx, run.req.TaskID, run.recording)
	return nil
}

// runWithInbox closes acceptance and drains late input within one execution budget.
func runWithInbox(ctx context.Context, ag core.Agent, taskID string, maxSteps int, owner *session.Session) (*core.Result, error) {
	return newRunInbox(ctx, taskID, owner).run(ctx, ag, maxSteps)
}

func (inbox *runInbox) run(ctx context.Context, ag core.Agent, maxSteps int) (*core.Result, error) {
	defer inbox.close()
	inbox.mu.Lock()
	if inbox.closed || ctx.Err() != nil {
		inbox.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("worker inbox is closed")
	}
	for _, message := range inbox.pending {
		ag.Append(message)
	}
	inbox.pending = nil
	inbox.agent = ag
	inbox.mu.Unlock()
	result, err := ag.ThinkAct(ctx)
	inbox.mu.Lock()
	for {
		unread := ag.Pending() > 0
		if !unread {
			messages := ag.Messages()
			unread = len(messages) > 0 && messages[len(messages)-1].Role == ai.RoleUser
		}
		if !unread {
			break
		}
		if result != nil && result.StopReason == core.StopEndTurn && maxSteps > 0 && result.Steps >= maxSteps {
			result.StopReason = core.StopMaxSteps
		}
		if err != nil || ctx.Err() != nil || inbox.closed || result == nil || result.StopReason != core.StopEndTurn {
			inbox.accepting = false
			inbox.mu.Unlock()
			// A canceled exchange settles accepted input into native history without inference.
			if ag.Pending() > 0 {
				flushCtx, cancel := context.WithCancel(ctx)
				cancel()
				_, _ = ag.ThinkAct(flushCtx)
			}
			if result != nil {
				result.Messages = ag.Messages()
			}
			return result, err
		}
		if maxSteps > 0 {
			ag.SetMaxSteps(maxSteps - result.Steps)
		}
		inbox.mu.Unlock()
		next, nextErr := ag.ThinkAct(ctx)
		if next != nil && result != nil {
			next.Steps += result.Steps
			next.ToolUses += result.ToolUses
			next.Usage.Add(result.Usage)
		}
		result, err = next, nextErr
		inbox.mu.Lock()
	}
	inbox.accepting = false
	inbox.mu.Unlock()
	return result, err
}
