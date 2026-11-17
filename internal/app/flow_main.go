package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/session"
)

// The bridge owns registration only. All model access stays on the UI loop.
type flowMainBridge struct {
	mu         sync.Mutex
	owner      *session.Session
	generation uint64
	lease      *flowMainLease
	events     chan flowMainMsg
}
type flowMainExecutor struct {
	bridge     *flowMainBridge
	owner      *session.Session
	generation uint64
}
type flowMainLease struct {
	bridge       *flowMainBridge
	owner        *session.Session
	generation   uint64
	done         chan struct{}
	instructions string
	capture      bool
	closeOnce    sync.Once
	drain        <-chan core.Event
	deferred     []pendingMessage // UI-owned ordinary input, not writer requirements.
}

type flowMainOp uint8

const (
	flowMainRun flowMainOp = iota
	flowMainReview
	flowMainCancel
	flowMainClose
)

type flowMainReply struct {
	output       string
	revise       bool
	err          error
	finalization *core.TurnFinalization
}
type flowMainMsg struct {
	op       flowMainOp
	lease    *flowMainLease
	ctx      context.Context
	call     *flow.Call
	accepted bool
	err      error
	reply    chan flowMainReply
}
type flowMainRequest struct {
	msg       flowMainMsg
	messageID string
	outbox    <-chan core.Event
	input     *flowMainInput
}

func newFlowMainBridge() *flowMainBridge { return &flowMainBridge{events: make(chan flowMainMsg, 64)} }
func (b *flowMainBridge) bind(owner *session.Session) flowMainExecutor {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner != owner {
		if b.lease != nil {
			close(b.lease.done)
			b.lease = nil
		}
		b.owner = owner
		b.generation++
	}
	return flowMainExecutor{b, owner, b.generation}
}
func (e flowMainExecutor) Open(ctx context.Context, info flow.MainRunInfo) (flow.MainRunExecutor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := e.bridge
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.owner == nil || b.owner != e.owner || b.generation != e.generation {
		return nil, errors.New("main session replaced")
	}
	if info.SessionID != e.owner.ID() {
		return nil, errors.New("Flow main session does not match opened owner")
	}
	if b.lease != nil {
		return nil, errors.New("main session already has a Flow writer")
	}
	l := &flowMainLease{bridge: b, owner: e.owner, generation: e.generation, done: make(chan struct{}), capture: true, instructions: info.Instructions}
	b.lease = l
	return l, nil
}
func (l *flowMainLease) Instructions() string {
	l.bridge.mu.Lock()
	defer l.bridge.mu.Unlock()
	return l.instructions
}
func (l *flowMainLease) Close(err error) {
	l.closeOnce.Do(func() {
		b := l.bridge
		b.mu.Lock()
		current := b.lease == l && b.owner == l.owner && b.generation == l.generation
		if current {
			l.capture = true
		}
		b.mu.Unlock()
		if current {
			select {
			case b.events <- flowMainMsg{op: flowMainClose, lease: l, err: err}:
			case <-l.done:
			}
		}
	})
}
func (l *flowMainLease) exchange(ctx context.Context, msg flowMainMsg) (flowMainReply, error) {
	if err := ctx.Err(); err != nil {
		return flowMainReply{}, err
	}
	msg.lease = l
	msg.ctx = ctx
	msg.reply = make(chan flowMainReply, 1)
	select {
	case <-ctx.Done():
		return flowMainReply{}, ctx.Err()
	case <-l.done:
		return flowMainReply{}, errors.New("main Flow lease retired")
	case l.bridge.events <- msg:
	}
	select {
	case r := <-msg.reply:
		return r, r.err
	case <-l.done:
		return flowMainReply{}, errors.New("main Flow lease retired")
	case <-ctx.Done():
		// Explicit cancellation is fenced by request identity again on the UI loop.
		select {
		case l.bridge.events <- flowMainMsg{op: flowMainCancel, lease: l, reply: msg.reply}:
		case <-l.done:
		}
		return flowMainReply{}, ctx.Err()
	}
}
func (l *flowMainLease) RunNode(ctx context.Context, c flow.Call) (string, error) {
	r, e := l.exchange(ctx, flowMainMsg{call: &c})
	if r.finalization != nil {
		if err := r.finalization.Wait(ctx); err != nil && err != core.ErrTurnCompleted {
			e = errors.Join(e, err)
		}
	}
	return r.output, e
}
func (l *flowMainLease) Review(ctx context.Context, accepted bool) (bool, error) {
	r, e := l.exchange(ctx, flowMainMsg{op: flowMainReview, accepted: accepted})
	return r.revise, e
}
func awaitFlowMain(b *flowMainBridge) tea.Cmd {
	if b == nil {
		return nil
	}
	return func() tea.Msg { return <-b.events }
}
func (m *model) flowMainLease() *flowMainLease {
	if m.flowMain == nil {
		return nil
	}
	b := m.flowMain
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner != m.services.Session {
		return nil
	}
	return b.lease
}
func (m *model) flowMainCapturing() bool {
	l := m.flowMainLease()
	if l == nil {
		return false
	}
	l.bridge.mu.Lock()
	defer l.bridge.mu.Unlock()
	return l.capture
}
func (m *model) deferFlowMainInput(p pendingMessage) bool {
	l := m.flowMainLease()
	if l == nil || p.FromUser {
		return false
	}
	if r := m.flowMainRequest; r != nil && r.msg.lease == l && r.messageID != "" && r.messageID == p.ID {
		return false
	}
	if p.Destination != nil && p.Destination != l.owner {
		return false
	}
	for _, existing := range l.deferred {
		if p.ID != "" && existing.ID == p.ID {
			return true
		}
	}
	l.deferred = append(l.deferred, p)
	return true
}

func (m *model) flowMainBlocksQueue() bool { return m.flowMainCapturing() && !m.flowMainWriterActive() }
func (m *model) flowMainWriterActive() bool {
	r := m.flowMainRequest
	return r != nil && r.messageID != "" && r.msg.lease == m.flowMainLease() && r.outbox == m.AgentOutbox()
}
func replyFlowMain(msg flowMainMsg, r flowMainReply) {
	if msg.reply != nil {
		msg.reply <- r
	}
}
func (m *model) onFlowMain(msg flowMainMsg) tea.Cmd {
	if msg.lease != m.flowMainLease() || msg.ctx != nil && msg.ctx.Err() != nil {
		if msg.op == flowMainRun || msg.op == flowMainReview {
			replyFlowMain(msg, flowMainReply{err: errors.New("main Flow request retired")})
		}
		return nil
	}
	switch msg.op {
	case flowMainClose:
		l := msg.lease
		var cancel tea.Cmd
		if r := m.flowMainRequest; r != nil && r.msg.lease == l {
			failure := msg.err
			if failure == nil {
				failure = errors.New("Flow closed before main node finalized")
			}
			m.failFlowMain(failure)
			if r.messageID != "" && r.outbox == m.AgentOutbox() && l.owner == m.services.Session {
				cancel = m.handleStreamCancel()
			}
		}
		l.bridge.mu.Lock()
		drain := l.drain
		close(l.done)
		l.bridge.lease = nil
		l.bridge.mu.Unlock()
		deferred := l.deferred
		l.deferred = nil
		if msg.err != nil || cancel != nil {
			for _, p := range deferred {
				m.pendingMessages.Enqueue(p)
			}
			m.pendingMessages.Hold(nil)
			return cancel
		}
		var cmds []tea.Cmd
		if drain != nil && drain == m.AgentOutbox() && l.owner == m.services.Session {
			cmds = append(cmds, m.sendNextQueuedInputIfIdle())
		}
		for _, p := range deferred {
			cmds = append(cmds, m.submit(p))
		}
		return tea.Batch(cmds...)
	case flowMainCancel:
		if r := m.flowMainRequest; r != nil && r.msg.lease == msg.lease && r.msg.reply == msg.reply {
			m.pendingMessages.Hold(nil)
			m.failFlowMain(context.Canceled)
			if r.messageID != "" && r.outbox == m.AgentOutbox() && r.msg.lease.owner == m.services.Session {
				return m.handleStreamCancel()
			}
		}
		return nil
	case flowMainReview:
		revise := len(m.flowMainAmendments()) > 0 || m.pendingMessages.Len() > 0
		if msg.accepted && !revise {
			msg.lease.bridge.mu.Lock()
			msg.lease.capture = false
			msg.lease.bridge.mu.Unlock()
		}
		replyFlowMain(msg, flowMainReply{revise: revise})
		return nil
	default:
		if m.flowMainRequest != nil {
			replyFlowMain(msg, flowMainReply{err: errors.New("main Flow node already pending")})
			return nil
		}
		m.flowMainRequest = &flowMainRequest{msg: msg}
		return m.startFlowMainNode()
	}
}

func (m *model) failFlowMain(err error) {
	if r := m.flowMainRequest; r != nil {
		m.flowMainRequest = nil
		replyFlowMain(r.msg, flowMainReply{err: err})
	}
}
func (m *model) startFlowMainNode() tea.Cmd {
	r := m.flowMainRequest
	if r != nil && r.messageID != "" {
		return m.confirmFlowMainInput()
	}
	if r == nil || m.conv.Stream.Active || m.conv.Compact.Active || m.pendingMessages.Cancelling() != nil || m.userInput.Queue.SelectIdx == 0 {
		return nil
	}
	if r.msg.lease != m.flowMainLease() || r.msg.ctx.Err() != nil {
		m.failFlowMain(errors.New("main Flow node retired"))
		return nil
	}
	if m.pendingMessages.Len() > 0 || m.pendingMessages.Held() {
		m.failFlowMain(errors.New("main input is held; explicitly resolve pending input before starting Flow"))
		return nil
	}
	message, start, display, err := m.flowMainWriterInput(r.msg.call.Prompt)
	if err != nil {
		m.pendingMessages.Hold(nil)
		m.failFlowMain(err)
		return start
	}
	r.messageID = message.ID
	r.outbox = m.AgentOutbox()
	if r.msg.call.OnExecution != nil {
		r.msg.call.OnExecution(flow.Execution{SessionID: r.msg.lease.owner.ID(), MessageID: message.ID})
	}
	if err := r.msg.ctx.Err(); err != nil {
		m.pendingMessages.Hold(nil)
		m.failFlowMain(err)
		return start
	}
	send := m.sendToAgent(pendingMessage{Message: message})
	if m.pendingMessages.Len() > 0 {
		m.discardPendingMessages()
		m.pendingMessages.Hold(nil)
		m.failFlowMain(errors.New("main writer input was not admitted"))
		return tea.Batch(start, send)
	}
	r.input = &flowMainInput{messageID: message.ID, commit: display}
	return tea.Batch(start, m.confirmFlowMainInput(), send)
}
func (m *model) retireFlowMain() {
	if l := m.flowMainLease(); l != nil {
		for _, p := range l.deferred {
			if p.Completion != nil {
				_ = p.Completion.Close()
			}
		}
		l.deferred = nil
	}
	if m.flowMain != nil {
		m.flowMain.bind(nil)
	}
	m.failFlowMain(errors.New("main session retired"))
}

func (m *model) finishFlowMain(result core.Result, err error) bool {
	r := m.flowMainRequest
	if r == nil || r.messageID == "" || r.outbox != m.AgentOutbox() || r.msg.lease.owner != m.services.Session {
		return false
	}
	m.flowMainRequest = nil
	if err == nil && result.StopReason != core.StopEndTurn {
		err = fmt.Errorf("main writer stopped: %s", result.StopReason)
	}
	if err == nil {
		err = r.msg.ctx.Err()
	}
	if err != nil {
		m.pendingMessages.Hold(nil)
	}
	if err == nil && r.msg.call.Node != nil && len(r.msg.call.Node.Downstream()) == 0 {
		r.msg.lease.bridge.mu.Lock()
		r.msg.lease.drain = r.outbox
		r.msg.lease.bridge.mu.Unlock()
	}
	replyFlowMain(r.msg, flowMainReply{output: result.Content, err: err, finalization: result.Finalization})
	return true
}
