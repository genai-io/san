package app

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
)

// memoryInput is a preparation for the current queue head, not a main turn.
type memoryInput struct {
	owner  *session.Session
	outbox <-chan core.Event
	stores system.MemoryStores
	id     string
	cancel context.CancelFunc
	accept func(memoryInputMsg) tea.Cmd
}

type memoryInputMsg struct {
	input  *memoryInput
	result agent.PreparedMemory
	err    error
}

func (m *model) needsMemoryInput(head pendingMessage) bool {
	if !head.FromUser || head.MemoryReady || head.Completion != nil || head.Source != nil || head.Destination != nil ||
		m.services.Session == nil || strings.TrimSpace(head.Text()) == "" || conv.IsAgentEnvelope(head.Text()) {
		return false
	}
	return agent.NeedsMemoryPreparation(m.agentAutoMemory, head.Message)
}

func (m *model) prepareMemoryInput(head pendingMessage) tea.Cmd {
	return m.startMemoryInput(context.Background(), head, nil)
}

func (m *model) startMemoryInput(parent context.Context, head pendingMessage, accept func(memoryInputMsg) tea.Cmd) tea.Cmd {
	ctx, cancel := context.WithCancel(parent)
	r := &memoryInput{owner: m.services.Session, outbox: m.AgentOutbox(), stores: m.agentAutoMemory, id: head.ID, cancel: cancel, accept: accept}
	startSpinner := !m.needsSpinner()
	m.memoryInput = r
	provider, info := m.resolveJudge("")
	selector := agent.Recaller{Provider: provider, Model: info.ModelID, Effort: agent.LowestEffort(provider, m.services.LLM.Store(), info), Recording: r.owner}
	history := m.services.Agent.Messages()
	prepare := func() tea.Msg {
		result, err := selector.Prepare(ctx, r.stores, head.Message, history)
		if ctx.Err() != nil {
			closeMemoryInput(result.Completion)
			result.Completion = nil
		}
		return memoryInputMsg{input: r, result: result, err: err}
	}
	if startSpinner {
		return tea.Batch(prepare, m.conv.Spinner.Tick)
	}
	return prepare
}

func closeMemoryInput(c *session.Completion) {
	if err := c.Close(); err != nil {
		log.Logger().Error("close memory side", zap.Error(err))
	}
}

func (m *model) cancelMemoryInput() {
	if r := m.memoryInput; r != nil {
		m.memoryInput = nil
		r.cancel()
	}
}

func (m *model) onMemoryInput(msg memoryInputMsg) tea.Cmd {
	r := msg.input
	if r != m.memoryInput {
		closeMemoryInput(msg.result.Completion)
		return nil
	}
	m.cancelMemoryInput()
	if r.owner != m.services.Session || r.outbox != m.AgentOutbox() || !m.pendingMessages.CanDeliver(false) {
		closeMemoryInput(msg.result.Completion)
		return nil
	}
	if r.accept != nil {
		return r.accept(msg)
	}
	head := m.pendingMessages.First()
	if head.ID != r.id || m.pendingMessages.Len() == 0 {
		closeMemoryInput(msg.result.Completion)
		return nil
	}
	if r.stores != m.autoMemory() && !msg.result.Handled {
		closeMemoryInput(msg.result.Completion)
		m.agentReloadPending = true
		return m.sendPendingMessages()
	}
	if msg.result.Handled {
		m.pendingMessages.PopFirst()
		m.pendingPluginRoot = ""
		notice := msg.result.Notice
		if msg.err != nil {
			notice = "Auto Memory could not finish: " + msg.err.Error()
		}
		if notice == "" {
			notice = "No memory changes."
		}
		m.conv.AddNotice(notice)
		return tea.Batch(append(m.CommitMessages(), m.sendPendingMessages(), m.sendNextQueuedInputIfIdle())...)
	}
	if msg.err != nil {
		closeMemoryInput(msg.result.Completion)
		return func() tea.Msg { return agentSendFailedMsg{err: msg.err} }
	}
	head.Message, head.Completion, head.MemoryReady = msg.result.Message, msg.result.Completion, true
	m.pendingMessages.ReplaceFirst(head)
	return m.sendPendingMessages()
}
