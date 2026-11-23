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

// memoryReview owns the proposal and its write on the original session.
type memoryReview struct {
	owner  *session.Session
	store  system.MemoryStore
	manual bool
	ctx    context.Context
	cancel context.CancelFunc
}

type memoryReviewProposed struct {
	review *memoryReview
	change *automemory.Change
	err    error
}

type memoryReviewFinished struct {
	review *memoryReview
	notice string
	err    error
}

func (m *model) autoMemoryUpdate() bool {
	return m.services.Setting != nil && m.services.Setting.MemoryUpdate()
}

func (m *model) updateMemory() (string, tea.Cmd) {
	if m.memoryReview != nil {
		return "Memory update is already running.", nil
	}
	if m.conv.Stream.Active {
		return "Wait for the current reply before updating memory.", nil
	}
	cmd := m.startMemoryReview(true)
	if cmd == nil {
		return "Memory update needs an enabled store and a completed reply on the current model.", nil
	}
	return "", cmd
}

func (m *model) reviewTurnMemory(result core.Result) tea.Cmd {
	if result.StopReason != core.StopEndTurn || result.Steps == 0 || !m.autoMemoryUpdate() {
		return nil
	}
	return m.startMemoryReview(false)
}

func (m *model) startMemoryReview(manual bool) tea.Cmd {
	owner, provider := m.services.Session, m.env.LLMProvider
	if m.memoryReview != nil || m.services.Setting == nil || owner == nil || provider == nil {
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
	r := &memoryReview{owner: owner, store: store, manual: manual, ctx: ctx, cancel: cancel}
	m.memoryReview = r
	maintainer := automemory.Maintainer{Provider: provider, Model: model, Recording: owner}
	return func() tea.Msg {
		req, err := prefix.Request(ctx, owner)
		if err != nil {
			return memoryReviewProposed{review: r, err: err}
		}
		change, err := maintainer.Review(ctx, store, req, automemory.Actions{Add: true, Update: true, Forget: true})
		if ctx.Err() != nil && change != nil {
			err = change.Discard()
			change = nil
		}
		return memoryReviewProposed{review: r, change: change, err: err}
	}
}

func (m *model) acceptMemoryReview(msg memoryReviewProposed) tea.Cmd {
	r := msg.review
	if r != m.memoryReview || r.owner != m.services.Session || r.ctx.Err() != nil ||
		r.store != m.autoMemoryStore() || (!r.manual && !m.autoMemoryUpdate()) {
		if err := msg.change.Discard(); err != nil {
			log.Logger().Warn("discard memory review", zap.Error(err))
		}
		if r == m.memoryReview {
			m.cancelMemoryReview()
		}
		return nil
	}
	if msg.err != nil || msg.change == nil {
		return m.finishMemoryReview(memoryReviewFinished{review: r, err: msg.err})
	}
	// The Aside performs the guarded write on its own worker, not in the main
	// model's loop or in the terminal event handler.
	return func() tea.Msg {
		notice, err := msg.change.Apply(r.ctx)
		return memoryReviewFinished{review: r, notice: notice, err: err}
	}
}

func (m *model) finishMemoryReview(msg memoryReviewFinished) tea.Cmd {
	if msg.review != m.memoryReview {
		return nil
	}
	m.cancelMemoryReview()
	switch {
	case msg.err != nil:
		log.Logger().Warn("memory review failed", zap.Error(msg.err))
		if msg.review.manual {
			m.conv.AddNotice("Auto Memory could not finish: " + msg.err.Error())
		}
	case msg.notice != "":
		m.addRecordedNotice(core.ChatMessage{Content: msg.notice, AutoMemoryMaintained: true})
	case msg.review.manual:
		m.conv.AddNotice("No memory changes.")
	}
	return tea.Batch(m.CommitMessages()...)
}

func (m *model) cancelMemoryReview() {
	if r := m.memoryReview; r != nil {
		m.memoryReview = nil
		r.cancel()
	}
}
