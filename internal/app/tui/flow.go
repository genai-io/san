package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/session"
)

// flowDraftTimeout bounds the one-shot drafting completion.
const flowDraftTimeout = 2 * time.Minute

type flowDraftRequest struct {
	owner      *session.Session
	generation uint64
	cancel     context.CancelFunc
}

type flowDraftedMsg struct {
	request   *flowDraftRequest
	recording *session.Completion
	source    string
	err       error
}

func (m *model) syncFlowDraft() {
	if r := m.flowDraft; r != nil && (r.owner != m.services.Session || !m.userInput.Flow.IsGenerating(r.generation)) {
		r.cancel()
		m.flowDraft = nil
	}
}

func (m *model) generateFlow(msg input.FlowDraftRequestMsg) tea.Cmd {
	m.syncFlowDraft()
	if !m.userInput.Flow.IsGenerating(msg.Generation) {
		return nil
	}
	if m.flowDraft != nil {
		if m.flowDraft.generation == msg.Generation && m.flowDraft.owner == m.services.Session {
			return nil
		}
		m.flowDraft.cancel()
		m.flowDraft = nil
	}
	if m.env.LLMProvider == nil {
		m.userInput.Flow.DraftReady(msg.Generation, "", fmt.Errorf("select a provider before generating a Flow"))
		return nil
	}
	catalog := ""
	if m.services.Subagent != nil {
		catalog = m.services.Subagent.AgentCatalog()
	}
	if m.services.Skill != nil {
		for _, sk := range m.services.Skill.ListEnabled() {
			catalog += "\nAvailable Skill: " + sk.FullName()
		}
	}
	req := m.newAsideRequest(session.PurposeFlow, false, flow.AuthoringPrompt(catalog, msg.Goal))
	ctx, cancel := context.WithTimeout(context.Background(), flowDraftTimeout)
	pending := &flowDraftRequest{owner: req.owner, generation: msg.Generation, cancel: cancel}
	var tick tea.Cmd
	if !m.needsSpinner() {
		tick = m.conv.Spinner.Tick
	}
	m.flowDraft = pending
	return tea.Batch(tick, func() tea.Msg {
		defer cancel()
		resp, err := req.complete(ctx)
		source := ""
		if err == nil {
			if len(resp.Content.ToolCalls()) != 0 {
				err = fmt.Errorf("Flow drafting returned a tool call; no tools were executed")
			} else if resp.StopReason != ai.StopEndTurn {
				err = fmt.Errorf("Flow drafting did not complete (stop reason: %q); no draft was created", resp.StopReason)
			} else {
				source = flow.UnwrapDraft(resp.Content.Text())
				if strings.TrimSpace(source) == "" {
					err = fmt.Errorf("Flow drafting returned empty text")
				}
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		return flowDraftedMsg{request: pending, recording: req.snapshot.Recording(), source: source, err: err}
	})
}

func (m *model) onFlowDrafted(msg flowDraftedMsg) tea.Cmd {
	r := msg.request
	if r == nil || m.flowDraft != r || r.owner != m.services.Session || !m.userInput.Flow.IsGenerating(r.generation) {
		closeAside(msg.recording)
		return nil
	}
	r.cancel()
	m.flowDraft = nil
	if msg.err != nil {
		closeAside(msg.recording)
	} else if !finishAside(msg.recording, "") {
		msg.err = fmt.Errorf("could not finish Flow draft recording")
	}
	// Validation and the unsaved Review editor belong to FlowView. Even malformed
	// source is handed over intact; this host never saves, launches, or repairs it.
	m.userInput.Flow.DraftReady(r.generation, msg.source, msg.err)
	return nil
}
