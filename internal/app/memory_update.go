package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/automemory"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
)

// memoryUpdate owns the proposal and its write on the original session.
type memoryUpdate struct {
	owner  *session.Session
	store  system.MemoryStore
	manual bool
	ctx    context.Context
	cancel context.CancelFunc
}

type memoryUpdateProposed struct {
	update *memoryUpdate
	change *automemory.Change
	err    error
}

type memoryUpdateFinished struct {
	update *memoryUpdate
	notice string
	err    error
}

func (m *model) autoMemoryUpdateEnabled() bool {
	return m.services.Setting != nil && m.services.Setting.MemoryUpdate()
}

func (m *model) updateMemory() (string, tea.Cmd) {
	if m.memoryUpdate != nil {
		return "Memory update is already running.", nil
	}
	if m.conv.Stream.Active {
		return "Wait for the current reply before updating memory.", nil
	}
	cmd := m.startMemoryUpdate(true)
	if cmd == nil {
		return "Memory update needs an enabled store and a completed reply on the current model.", nil
	}
	return "", cmd
}

func (m *model) updateMemoryAfterTurn(result core.Result) tea.Cmd {
	if result.StopReason != core.StopEndTurn || result.Steps == 0 || !m.autoMemoryUpdateEnabled() {
		return nil
	}
	return m.startMemoryUpdate(false)
}

func (m *model) startMemoryUpdate(manual bool) tea.Cmd {
	owner, provider := m.services.Session, m.env.LLMProvider
	if m.memoryUpdate != nil || m.services.Setting == nil || owner == nil || provider == nil {
		return nil
	}
	store := m.autoMemoryStore()
	if store.Off() {
		return nil
	}
	model := m.env.GetModelID()
	snapshot := owner.Snapshot()
	prefix, ok := snapshot.LastCompletedTurn(provider.Name(), model)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	r := &memoryUpdate{owner: owner, store: store, manual: manual, ctx: ctx, cancel: cancel}
	m.memoryUpdate = r
	updater := automemory.Updater{Provider: provider, Model: model, Recording: owner}
	return func() tea.Msg {
		req, err := prefix.Request(ctx, owner)
		if err != nil {
			return memoryUpdateProposed{update: r, err: err}
		}
		change, err := updater.Propose(ctx, store, req)
		if ctx.Err() != nil && change != nil {
			err = change.Discard()
			change = nil
		}
		return memoryUpdateProposed{update: r, change: change, err: err}
	}
}

func (m *model) applyMemoryUpdate(msg memoryUpdateProposed) tea.Cmd {
	r := msg.update
	if r != m.memoryUpdate || r.owner != m.services.Session || r.ctx.Err() != nil ||
		r.store != m.autoMemoryStore() || (!r.manual && !m.autoMemoryUpdateEnabled()) {
		if err := msg.change.Discard(); err != nil {
			log.Logger().Warn("discard memory update proposal", zap.Error(err))
		}
		if r == m.memoryUpdate {
			m.cancelMemoryUpdate()
		}
		return nil
	}
	if msg.err != nil || msg.change == nil {
		return m.finishMemoryUpdate(memoryUpdateFinished{update: r, err: msg.err})
	}
	// Apply the guarded write off the model event loop.
	return func() tea.Msg {
		notice, err := msg.change.Apply(r.ctx)
		return memoryUpdateFinished{update: r, notice: notice, err: err}
	}
}

func (m *model) finishMemoryUpdate(msg memoryUpdateFinished) tea.Cmd {
	if msg.update != m.memoryUpdate {
		return nil
	}
	m.cancelMemoryUpdate()
	switch {
	case msg.err != nil:
		log.Logger().Warn("memory update failed", zap.Error(msg.err))
		if msg.update.manual {
			m.conv.AddNotice("Auto Memory could not finish: " + msg.err.Error())
		}
	case msg.notice != "":
		m.addRecordedNotice(core.ChatMessage{Content: msg.notice, AutoMemoryUpdated: true})
	case msg.update.manual:
		m.conv.AddNotice("No memory changes.")
	}
	return tea.Batch(m.CommitMessages()...)
}

func (m *model) cancelMemoryUpdate() {
	if r := m.memoryUpdate; r != nil {
		m.memoryUpdate = nil
		r.cancel()
	}
}
