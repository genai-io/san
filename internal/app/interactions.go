package app

import (
	"context"
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/conversation"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
)

type interactionState struct {
	queue      conversation.Queue
	active     *conv.InteractionRequest
	shown      bool
	permission *perm.PermissionRequest
}

func (m *model) validInteraction(req *conv.InteractionRequest) bool {
	return req.Valid(m.conv.AgentToUI)
}

func (m *model) enqueueInteraction(req *conv.InteractionRequest) tea.Cmd {
	if !m.interactions.queue.Enqueue(req, m.conv.AgentToUI) {
		return nil
	}
	defer m.refreshTaskViewer()
	return m.advanceInteractions()
}

func cancelInteraction(req *conv.InteractionRequest) {
	req.Cancel()
}

// pruneInteractions runs only on the UI goroutine. A turn ending cancels its
// foreground descendants, not independent background requests with live contexts.
func (m *model) pruneInteractions() {
	queueChanged := m.interactions.queue.Prune(m.conv.AgentToUI)
	if req := m.interactions.active; req != nil && !m.validInteraction(req) {
		cancelInteraction(req)
		m.finishInteraction()
	} else if queueChanged {
		m.refreshTaskViewer()
	}
}

func (m *model) advanceInteractions() tea.Cmd {
	defer m.syncApprovalToolState()
	m.pruneInteractions()
	if m.userInput.Tasks.ActionPending() || m.tasks.stopID != 0 {
		return nil
	}
	if m.interactions.active == nil {
		if _, overlay := m.activeOverlay(); overlay || len(m.interactions.queue) == 0 {
			return nil
		}
		m.interactions.active = m.interactions.queue.Pop()
	}
	return m.showActiveInteraction()
}

// A permission prompt can arrive before its tool batch's outbox event. The
// later Track resets tool state, so reassert the active request's exact identity
// rather than briefly presenting a blocked Agent as pending or running.
func (m *model) syncApprovalToolState() {
	request := m.interactions.active
	if !m.interactions.shown || !m.validInteraction(request) || request.Permission == nil {
		return
	}
	callID := request.Permission.ToolCallID
	if request.Source.AgentID != "" {
		callID = request.Source.ParentToolCallID
	} else if callID == "" {
		callID = m.unambiguousApprovalCallID(request)
	}
	m.conv.Tool.MarkAwaitingApproval(callID)
}

// Older adapters may omit the context's call ID. For display only, recover it
// from an exact, unique tool-name/input match; never guess by title or position.
func (m *model) unambiguousApprovalCallID(request *conv.InteractionRequest) string {
	args := make(map[string]any, len(request.Permission.Input))
	for key, value := range request.Permission.Input {
		if !strings.HasPrefix(key, "_") {
			args[key] = value
		}
	}
	want, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	id := ""
	for _, call := range m.conv.Tool.PendingCalls {
		if call.Name != request.Permission.ToolName {
			continue
		}
		var input map[string]any
		if json.Unmarshal([]byte(call.Input), &input) != nil {
			continue
		}
		got, err := json.Marshal(input)
		if err == nil && string(got) == string(want) {
			if id != "" {
				return ""
			}
			id = call.ID
		}
	}
	return id
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
			var err error
			view, err = m.preparePermissionRequest(req.Permission)
			if err != nil {
				select {
				case req.Permission.Response <- agent.PermGateResponse{Reason: "cannot prepare " + req.Permission.ToolName + " approval: " + err.Error()}:
				default:
				}
				m.finishInteraction()
				return m.advanceInteractions()
			}
		}
		m.interactions.permission = view
		if req.Source.AgentID != "" {
			view.OnceOnly = true
			view.AllowRules = nil
		}
		m.syncApprovalToolState()
		if rec := m.services.Session; rec != nil {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionRequired, Permission: &transcript.PermissionRecord{
				RequestID: req.Permission.RequestID, Tool: req.Permission.ToolName,
				Input: marshalPermInput(req.Permission.Input), Detail: interactionPermissionDetail(req, view),
				OptionsOffered: input.BuildApprovalOptions(view), Source: transcript.PermissionSourceAsk,
				Mode: req.Source.PermissionMode,
			}})
		}
		m.userInput.Approval.Show(view, m.env.Width, m.env.Height)
		m.resizeApprovalPanel()
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

func (m *model) resizeApprovalPanel() {
	if !m.userInput.Approval.IsActive() {
		return
	}
	reserved := 2 // blank row and dock separator
	if m.interactionQueueContext() != "" {
		reserved++
	}
	m.userInput.Approval.Resize(max(1, m.env.Width-1), max(1, m.env.Height-reserved))
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
	m.interactions.queue.Cancel()
	m.finishInteraction()
}

func (m *model) resetInteractions() {
	m.closeInteractions()
	m.conv.AgentToUI = conv.NewAgentToUI(100)
}
