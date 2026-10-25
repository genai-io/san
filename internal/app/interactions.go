package app

import (
	"context"
	"encoding/json"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
)

type interactionState struct {
	queue      []*conv.InteractionRequest
	active     *conv.InteractionRequest
	shown      bool
	permission *perm.PermissionRequest
}

func (m *model) validInteraction(req *conv.InteractionRequest) bool {
	return req != nil && (req.Context == nil || req.Context.Err() == nil) &&
		(req.Owner == nil || (req.Owner == m.conv.AgentToUI && !req.Owner.Closed()))
}

func (m *model) enqueueInteraction(req *conv.InteractionRequest) tea.Cmd {
	if !m.validInteraction(req) {
		cancelInteraction(req)
		return nil
	}
	m.interactions.queue = append(m.interactions.queue, req)
	defer m.refreshTaskViewer()
	return m.advanceInteractions()
}

func cancelInteraction(req *conv.InteractionRequest) {
	if req == nil {
		return
	}
	switch {
	case req.Permission != nil:
		select {
		case req.Permission.Response <- agent.PermGateResponse{Reason: "interaction cancelled"}:
		default:
		}
	case req.Question != nil:
		select {
		case req.Question.Reply <- &tool.QuestionResponse{RequestID: req.Question.Request.ID, Cancelled: true}:
		default:
		}
	case req.Secret != nil:
		select {
		case req.Secret.Reply <- conv.SecretPromptResponse{Cancelled: true}:
		default:
		}
	}
}

// pruneInteractions runs only on the UI goroutine. A turn ending cancels its
// foreground descendants, not independent background requests with live contexts.
func (m *model) pruneInteractions() {
	queue := m.interactions.queue[:0]
	for _, req := range m.interactions.queue {
		if m.validInteraction(req) {
			queue = append(queue, req)
		} else {
			cancelInteraction(req)
		}
	}
	queueChanged := len(queue) != len(m.interactions.queue)
	clear(m.interactions.queue[len(queue):])
	m.interactions.queue = queue
	if req := m.interactions.active; req != nil && !m.validInteraction(req) {
		cancelInteraction(req)
		m.finishInteraction()
	} else if queueChanged {
		m.refreshTaskViewer()
	}
}

func (m *model) advanceInteractions() tea.Cmd {
	m.pruneInteractions()
	if m.userInput.Tasks.ActionPending() || m.tasks.stopID != 0 {
		return nil
	}
	if m.interactions.active == nil {
		if _, overlay := m.activeOverlay(); overlay || len(m.interactions.queue) == 0 {
			return nil
		}
		m.interactions.active = m.interactions.queue[0]
		m.interactions.queue[0] = nil
		m.interactions.queue = m.interactions.queue[1:]
	}
	return m.showActiveInteraction()
}

func (m *model) showActiveInteraction() tea.Cmd {
	req := m.interactions.active
	if req == nil || m.interactions.shown || len(m.flush.pendingPrints) > 0 {
		return nil
	}
	if _, overlay := m.activeOverlay(); overlay {
		return nil
	}
	m.interactions.shown = true
	switch {
	case req.Permission != nil:
		view := req.PermissionView
		if view == nil {
			view = m.preparePermissionRequest(req.Permission)
		}
		m.interactions.permission = view
		callID := req.Permission.ToolCallID
		if req.Source.AgentID != "" {
			callID = req.Source.ParentToolCallID
			view.OnceOnly = true
			view.AllowRules = nil
		}
		m.conv.Tool.MarkAwaitingApproval(callID)
		if rec := m.services.Session; rec != nil {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionRequired, Permission: &transcript.PermissionRecord{
				RequestID: req.Permission.RequestID, Tool: req.Permission.ToolName,
				Input: marshalPermInput(req.Permission.Input), Detail: interactionPermissionDetail(req, view),
				OptionsOffered: input.BuildApprovalOptions(view), Source: transcript.PermissionSourceAsk,
				Mode: req.Source.PermissionMode,
			}})
		}
		m.userInput.Approval.Show(view, m.env.Width, m.env.Height)
	case req.Question != nil:
		m.conv.Modal.Question.ShowFrom(req.Question.Request, m.env.Width, req.Source.Label())
		// Worker questions require the human; the parent's copilot cannot grant
		// or invent answers in a different worker's policy scope.
		if req.Source.AgentID == "" {
			return m.autopilotAnswerQuestionCmd(req.Question.Request)
		}
	case req.Secret != nil:
		m.userInput.Secret.ShowRequest(req.Secret.Prompt, m.env.Width, req.ID)
	}
	return nil
}

func interactionPermissionDetail(req *conv.InteractionRequest, view *perm.PermissionRequest) json.RawMessage {
	detail := permDetail(view)
	if req.Source.AgentID == "" {
		return detail
	}
	data, _ := json.Marshal(struct {
		Agent      tool.AgentInteractionSource `json:"agent"`
		Permission json.RawMessage             `json:"permission,omitempty"`
	}{req.Source, detail})
	return data
}

func (m *model) finishInteraction() {
	if req := m.interactions.active; req != nil {
		switch {
		case req.Permission != nil:
			m.userInput.Approval.Hide()
			m.conv.Tool.ClearAwaitingApproval()
		case req.Question != nil:
			m.conv.Modal.Question.Hide()
		case req.Secret != nil:
			m.userInput.Secret.Hide()
		}
	}
	finished := m.interactions.active
	m.interactions.active = nil
	m.interactions.permission = nil
	m.interactions.shown = false
	if finished != nil && finished == m.tasks.review {
		m.resumeTaskViewer()
	} else if finished != nil {
		m.refreshTaskViewer()
	}
}

func (m *model) closeInteractions() {
	m.hideTaskViewer()
	m.conv.AgentToUI.Close()
	cancelInteraction(m.interactions.active)
	for _, req := range m.interactions.queue {
		cancelInteraction(req)
	}
	m.finishInteraction()
	m.interactions.queue = nil
}

func (m *model) resetInteractions() {
	m.closeInteractions()
	m.conv.AgentToUI = conv.NewAgentToUI(100)
}
