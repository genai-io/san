package app

import (
	"context"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

// memoryInput is a preparation for the current queue head, not a main turn.
type memoryInput struct {
	owner       *session.Session
	outbox      <-chan core.Event
	stores      system.MemoryStores
	head        pendingMessage
	parent      context.Context
	cancel      context.CancelFunc
	accept      func(memoryInputMsg) tea.Cmd
	startedAt   time.Time
	maintenance bool
}

type memoryInputMsg struct {
	input  *memoryInput
	result agent.PreparedMemory
	err    error
}

type autoMemoryConfig struct {
	stores system.MemoryStores
	recall setting.MemoryRecallConfig
}

func (m *model) currentAutoMemory() autoMemoryConfig {
	return autoMemoryConfig{stores: m.autoMemoryStores(), recall: m.memoryRecallConfig()}
}

func (m *model) memoryRecallConfig() setting.MemoryRecallConfig {
	if m.services.Setting == nil {
		return setting.MemoryRecallConfig{}
	}
	return m.services.Setting.MemoryRecall()
}

func (m *model) needsMemoryInput(head pendingMessage) bool {
	if !head.FromUser || head.MemoryReady || head.Completion != nil || head.Source != nil || head.Destination != nil ||
		m.services.Session == nil || strings.TrimSpace(head.Text()) == "" || conv.IsAgentEnvelope(head.Text()) {
		return false
	}
	return agent.IsMemoryMaintenance(head.Message) || (m.memoryRecallConfig().Mode == setting.MemoryRecallWait && m.autoMemory.stores.HasNotes())
}

func (m *model) prepareMemoryInput(head pendingMessage) tea.Cmd {
	return m.startMemoryInput(context.Background(), head, nil)
}

func (m *model) startMemoryInput(parent context.Context, head pendingMessage, accept func(memoryInputMsg) tea.Cmd) tea.Cmd {
	ctx, cancel := context.WithCancel(parent)
	r := &memoryInput{
		owner: m.services.Session, outbox: m.AgentOutbox(), stores: m.autoMemory.stores,
		head: head, parent: parent, cancel: cancel, accept: accept,
		startedAt: time.Now(), maintenance: agent.IsMemoryMaintenance(head.Message),
	}
	if r.maintenance {
		r.stores = m.autoMemoryStores()
	}
	startSpinner := !m.needsSpinner()
	m.memoryInput = r
	var provider llm.Provider
	var info *llm.CurrentModelInfo
	var effort string
	if r.maintenance && m.env.LLMProvider != nil {
		provider = m.env.LLMProvider
		info = &llm.CurrentModelInfo{ModelID: m.env.GetModelID()}
		effort = m.env.EffectiveThinkingEffort()
	} else {
		provider, info = m.resolveJudge("")
		effort = agent.LowestEffort(provider, m.services.LLM.Store(), info)
	}
	selector := agent.Recaller{Provider: provider, Model: info.ModelID, Effort: effort, Recording: r.owner, Timeout: m.memoryRecallConfig().Timeout}
	history := m.services.Agent.Messages()
	prepare := func() tea.Msg {
		result, err := selector.PrepareForAdmission(ctx, r.stores, head.Message, history)
		if ctx.Err() != nil {
			discardPreparedMemory(result)
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

func discardPreparedMemory(result agent.PreparedMemory) {
	closeMemoryInput(result.Completion)
	if err := result.Change.Discard(); err != nil {
		log.Logger().Error("discard memory change", zap.Error(err))
	}
}

// addRecordedNotice displays an annotation already persisted through its
// completion's owner, without invoking the conversation's OnNotice hook.
func (m *model) addRecordedNotice(notice string) {
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatNotice, Content: notice})
}

func (m *model) finishMemoryMaintenance(msg memoryInputMsg, showNoOp bool) {
	notice, err := msg.result.Notice, msg.err
	if msg.result.Change != nil {
		notice, err = msg.result.Change.Apply(msg.input.parent)
		if err == nil && notice != "" {
			m.addRecordedNotice(notice)
			return
		}
	}
	if err != nil {
		m.conv.AddNotice("Auto Memory could not finish: " + err.Error())
	} else if notice != "" {
		m.conv.AddNotice(notice)
	} else if showNoOp {
		m.conv.AddNotice("No memory changes.")
	}
}

type memoryAdmission uint8

const (
	memoryAdmit memoryAdmission = iota
	memoryStaleRecall
	memoryStaleMaintenance
	memoryMaintained
	memoryFailed
)

// resolveMemoryAdmission runs after the caller verifies it still owns the queue head.
// The caller decides how to resume its queue after each outcome.
func (m *model) resolveMemoryAdmission(msg memoryInputMsg, head *pendingMessage, showNoOp bool) memoryAdmission {
	if msg.input.stores != m.autoMemoryStores() {
		discardPreparedMemory(msg.result)
		if msg.result.Handled {
			return memoryStaleMaintenance
		}
		return memoryStaleRecall
	}
	if msg.result.Handled {
		m.finishMemoryMaintenance(msg, showNoOp)
		return memoryMaintained
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
		discardPreparedMemory(msg.result)
		return nil
	}
	m.cancelMemoryInput()
	if r.owner != m.services.Session || r.owner.Err() != nil || r.outbox != m.AgentOutbox() ||
		r.parent.Err() != nil || !m.pendingMessages.CanDeliver(false) {
		discardPreparedMemory(msg.result)
		return nil
	}
	if r.accept != nil {
		return r.accept(msg)
	}
	head := m.pendingMessages.First()
	if m.pendingMessages.Len() == 0 || !reflect.DeepEqual(head, r.head) {
		discardPreparedMemory(msg.result)
		return m.sendPendingMessages()
	}
	switch m.resolveMemoryAdmission(msg, &head, true) {
	case memoryStaleRecall:
		m.agentReloadPending = true
	case memoryMaintained:
		m.pendingMessages.PopFirst()
		m.pendingPluginRoot = ""
		return tea.Batch(append(m.CommitMessages(), m.sendPendingMessages(), m.sendNextQueuedInputIfIdle())...)
	case memoryFailed:
		return func() tea.Msg { return agentSendFailedMsg{err: msg.err} }
	case memoryAdmit:
		m.pendingMessages.ReplaceFirst(head)
		return m.sendPendingMessagesSince(r.startedAt)
	}
	return m.sendPendingMessages()
}
