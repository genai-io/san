package subagent

import (
	"context"
	"errors"
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
	taskID     string
	owner      *session.Session
	agent      core.Agent
	pending    []core.Message
	accepting  bool
	closed     bool
	unregister func()
	stop       func() bool
}

// newRunInbox registers an inbox that accepts mail immediately.
func newRunInbox(ctx context.Context, taskID string, owner *session.Session) *runInbox {
	return registerRunInbox(ctx, taskID, owner, true)
}

// newHeldRunInbox registers an inbox that rejects mail without touching history
// until open, so retained completions stay with the broker meanwhile.
func newHeldRunInbox(ctx context.Context, taskID string, owner *session.Session) *runInbox {
	return registerRunInbox(ctx, taskID, owner, false)
}

func registerRunInbox(ctx context.Context, taskID string, owner *session.Session, accepting bool) *runInbox {
	inbox := &runInbox{ctx: ctx, taskID: taskID, owner: owner, accepting: accepting}
	if taskID != "" {
		inbox.unregister = broker.RegisterAccept(taskID, inbox.deliver)
	}
	inbox.mu.Lock()
	inbox.stop = context.AfterFunc(ctx, inbox.close)
	inbox.mu.Unlock()
	return inbox
}

func (inbox *runInbox) deliver(m broker.Message) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if !inbox.accepting || inbox.closed || inbox.ctx.Err() != nil || (inbox.owner != nil && inbox.owner.Err() != nil) {
		return broker.ErrRejected
	}
	if inbox.queuedLocked() >= maxPendingWorkerMessages {
		return broker.ErrInboxFull
	}
	message := core.UserMessage(m.Content, nil)
	var source *session.EventRef
	if m.SourceSession != "" || m.SourceSeq != 0 {
		if inbox.owner == nil || m.SourceSession == "" || m.SourceSeq == 0 {
			return broker.ErrRejected
		}
		source = &session.EventRef{Session: m.SourceSession, Seq: m.SourceSeq}
	}
	if inbox.owner != nil {
		message.ID = core.NewMessageID()
		if err := inbox.owner.AcceptInput(inbox.ctx, message, source); err != nil {
			return broker.ErrRejected
		}
	}
	if inbox.agent == nil {
		inbox.pending = append(inbox.pending, message)
	} else {
		inbox.agent.Append(message)
	}
	return nil
}

func (inbox *runInbox) queuedLocked() int {
	queued := len(inbox.pending)
	if inbox.agent != nil {
		queued += inbox.agent.Pending()
	}
	return queued
}

// open starts accepting mail on a held inbox. It delivers nothing itself; call
// signalCapacity once no caller lock is held.
func (inbox *runInbox) open() {
	inbox.mu.Lock()
	inbox.accepting = true
	inbox.mu.Unlock()
}

// signalCapacity wakes retained completion mail when the inbox has room. It may
// deliver synchronously, so callers must not hold locks that delivery needs.
func (inbox *runInbox) signalCapacity() {
	inbox.mu.Lock()
	hasRoom := inbox.accepting && !inbox.closed && inbox.ctx.Err() == nil && inbox.queuedLocked() < maxPendingWorkerMessages
	inbox.mu.Unlock()
	if hasRoom && inbox.taskID != "" {
		broker.WakeCompletions(inbox.taskID)
	}
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
// A continuation's route starts held: acceptContinuationInput commits its input
// and opens the route while the task begins the next attempt.
func (e *Executor) prepareInbox(ctx context.Context, run *preparedRun) error {
	if e.tasks != nil {
		return nil // Session-owned hosts do not publish TUI broker addresses.
	}
	if err := e.openSession(ctx, run); err != nil {
		return err
	}
	if run.req.ResumeTaskID != "" {
		run.inbox = newHeldRunInbox(ctx, run.req.TaskID, run.recording)
		return nil
	}
	message := core.UserMessage(run.originalPrompt, nil)
	message.ID = core.NewMessageID()
	if err := run.recording.AcceptInput(ctx, message, nil); err != nil {
		if run.checkpoint != nil {
			run.checkpoint.persistenceErr = err
		}
		return errors.Join(err, run.recording.Close())
	}
	run.initialInput = &message
	run.inbox = newRunInbox(ctx, run.req.TaskID, run.recording)
	return nil
}

// run attaches ag, then closes acceptance and drains late input within one
// execution budget. When handoff approves the failure, the route stays live and
// later mail waits for the next run on this inbox instead of being rejected.
func (inbox *runInbox) run(ctx context.Context, ag core.Agent, maxSteps int, handoff func(error) bool) (*core.Result, error) {
	handedOff := false
	defer func() {
		if !handedOff {
			inbox.close()
		}
	}()
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
			handedOff = inbox.finishLocked(err, handoff)
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
	handedOff = inbox.finishLocked(err, handoff)
	inbox.mu.Unlock()
	return result, err
}

// finishLocked ends acceptance for this run unless handoff keeps the inbox for
// a replacement agent; then new mail queues as pending until it attaches.
func (inbox *runInbox) finishLocked(err error, handoff func(error) bool) bool {
	if err != nil && handoff != nil && !inbox.closed && handoff(err) {
		inbox.agent = nil
		return true
	}
	inbox.accepting = false
	return false
}
