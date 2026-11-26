package tui

import (
	"context"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/tui/transcript"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

// memoryInput is a preparation for the current queue head, not a main turn.
type memoryInput struct {
	owner     *session.Session
	outbox    <-chan core.Event
	store     system.MemoryStore
	head      pendingMessage
	parent    context.Context
	cancel    context.CancelFunc
	accept    func(memoryInputMsg) tea.Cmd
	startedAt time.Time
}

type memoryInputMsg struct {
	input  *memoryInput
	result agent.PreparedMemory
	err    error
}

func (m *model) currentAutoMemory() agent.AutoMemoryConfig {
	if svc := m.services.Setting; svc != nil {
		return agent.AutoMemoryConfig{Store: m.autoMemoryStore(), Recall: svc.MemoryRecall(), IndexLimits: svc.MemoryPreview()}
	}
	return agent.AutoMemoryConfig{}
}

func (m *model) memoryRecallConfig() setting.MemoryRecallConfig {
	if m.services.Setting == nil {
		return setting.MemoryRecallConfig{}
	}
	return m.services.Setting.MemoryRecall()
}

func (m *model) needsMemoryInput(head pendingMessage) bool {
	if !head.FromUser || head.MemoryReady || head.Completion != nil || head.Source != nil || head.Destination != nil ||
		m.services.Session == nil || strings.TrimSpace(head.Text()) == "" || transcript.IsAgentEnvelope(head.Text()) {
		return false
	}
	return m.memoryRecallConfig().Mode == setting.MemoryRecallWait && m.autoMemory.Store.HasNotes()
}

func (m *model) prepareMemoryInput(head pendingMessage) tea.Cmd {
	return m.startMemoryInput(context.Background(), head, nil)
}

func (m *model) startMemoryInput(parent context.Context, head pendingMessage, accept func(memoryInputMsg) tea.Cmd) tea.Cmd {
	ctx, cancel := context.WithCancel(parent)
	r := &memoryInput{
		owner: m.services.Session, outbox: m.AgentOutbox(), store: m.autoMemory.Store,
		head: head, parent: parent, cancel: cancel, accept: accept,
		startedAt: time.Now(),
	}
	startSpinner := !m.needsSpinner()
	m.memoryInput = r
	provider, info := m.resolveJudge("")
	effort := agent.LowestEffort(provider, m.services.LLM.Store(), info)
	selector := agent.Recaller{Provider: provider, Model: info.ModelID, Effort: effort, Recording: r.owner}
	if svc := m.services.Setting; svc != nil {
		selector.Timeout = svc.MemoryRecall().Timeout
		selector.IndexLimits = svc.MemoryPreview()
	}
	history := m.services.Agent.Messages()
	prepare := func() tea.Msg {
		result, err := selector.PrepareForAdmission(ctx, r.store, head.Message, history)
		if ctx.Err() != nil {
			closeMemoryInput(result.Completion)
			result = agent.PreparedMemory{}
		}
		return memoryInputMsg{input: r, result: result, err: err}
	}
	if startSpinner {
		return tea.Batch(prepare, m.conv.Spinner.Tick)
	}
	return prepare
}

func closeMemoryInput(c *session.Completion) {
	closeCompletion(c, "close memory side")
}

// addRecordedNotice displays an annotation already persisted through its
// completion's owner, without invoking the conversation's OnNotice hook.
func (m *model) addRecordedNotice(notice core.ChatMessage) {
	notice.Role = core.ChatNotice
	m.conv.Messages = append(m.conv.Messages, notice)
}

type memoryAdmission uint8

const (
	memoryAdmit memoryAdmission = iota
	memoryStaleRecall
	memoryFailed
)

// resolveMemoryAdmission runs after the caller verifies it still owns the queue head.
// The caller decides how to resume its queue after each outcome.
func (m *model) resolveMemoryAdmission(msg memoryInputMsg, head *pendingMessage) memoryAdmission {
	if msg.input.store != m.autoMemoryStore() {
		closeMemoryInput(msg.result.Completion)
		return memoryStaleRecall
	}
	if msg.err != nil {
		closeMemoryInput(msg.result.Completion)
		return memoryFailed
	}
	head.Message, head.Completion, head.MemoryReady = msg.result.Message, msg.result.Completion, true
	return memoryAdmit
}

func (m *model) cancelMemoryInput() {
	if r := m.memoryInput; r != nil {
		m.memoryInput = nil
		r.cancel()
	}
}

func (m *model) onMemoryInput(msg memoryInputMsg) tea.Cmd {
	r := msg.input
	if r == nil || r != m.memoryInput {
		closeMemoryInput(msg.result.Completion)
		return nil
	}
	m.cancelMemoryInput()
	if r.owner != m.services.Session || r.owner.Err() != nil || r.outbox != m.AgentOutbox() ||
		r.parent.Err() != nil || !m.pendingMessages.CanDeliver(false) {
		closeMemoryInput(msg.result.Completion)
		return nil
	}
	if r.accept != nil {
		return r.accept(msg)
	}
	head := m.pendingMessages.First()
	if m.pendingMessages.Len() == 0 || !reflect.DeepEqual(head, r.head) {
		closeMemoryInput(msg.result.Completion)
		return m.sendPendingMessages()
	}
	switch m.resolveMemoryAdmission(msg, &head) {
	case memoryStaleRecall:
		m.agentReloadPending = true
	case memoryFailed:
		return func() tea.Msg { return agentSendFailedMsg{err: msg.err} }
	case memoryAdmit:
		m.pendingMessages.ReplaceFirst(head)
		return m.sendPendingMessagesSince(r.startedAt)
	}
	return m.sendPendingMessages()
}
