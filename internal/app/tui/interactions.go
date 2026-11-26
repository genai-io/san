package tui

import (
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/ask"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/app/tui/transcript"
	"github.com/genai-io/san/internal/tool/perm"
)

type userPromptState struct {
	queue      ask.Queue
	active     *transcript.UserPromptRequest
	shown      bool
	permission *perm.PermissionRequest
	approval   *ask.Permission
}

func (m *model) validUserPrompt(req *transcript.UserPromptRequest) bool {
	return req.Valid(m.conv.AgentToUI)
}

func (m *model) enqueueUserPrompt(req *transcript.UserPromptRequest) tea.Cmd {
	if !m.userPrompts.queue.Enqueue(req, m.conv.AgentToUI) {
		return nil
	}
	defer m.refreshTaskViewer()
	return m.advanceUserPrompts()
}

func cancelUserPrompt(req *transcript.UserPromptRequest) {
	req.Cancel()
}

// pruneUserPrompts runs only on the UI goroutine. A turn ending cancels its
// foreground descendants, not independent background requests with live contexts.
func (m *model) pruneUserPrompts() {
	queueChanged := m.userPrompts.queue.Prune(m.conv.AgentToUI)
	if req := m.userPrompts.active; req != nil && !m.validUserPrompt(req) {
		cancelUserPrompt(req)
		m.finishUserPrompt()
	} else if queueChanged {
		m.refreshTaskViewer()
	}
}

func (m *model) advanceUserPrompts() tea.Cmd {
	defer m.syncApprovalToolState()
	m.pruneUserPrompts()
	if m.userInput.Tasks.ActionPending() || m.tasks.stopID != 0 {
		return nil
	}
	if m.userPrompts.active == nil {
		if _, overlay := m.activeOverlay(); overlay || len(m.userPrompts.queue) == 0 {
			return nil
		}
		m.userPrompts.active = m.userPrompts.queue.Pop()
	}
	return m.showActiveUserPrompt()
}

// A permission prompt can arrive before its tool batch's outbox event. The
// later Track resets tool state, so reassert the active request's exact identity
// rather than briefly presenting a blocked Agent as pending or running.
func (m *model) syncApprovalToolState() {
	request := m.userPrompts.active
	if !m.userPrompts.shown || !m.validUserPrompt(request) || request.Permission == nil {
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
func (m *model) unambiguousApprovalCallID(request *transcript.UserPromptRequest) string {
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

func (m *model) showActiveUserPrompt() tea.Cmd {
	req := m.userPrompts.active
	if req == nil || m.userPrompts.shown || len(m.flush.pendingPrints) > 0 {
		return nil
	}
	if _, overlay := m.activeOverlay(); overlay {
		return nil
	}
	m.userPrompts.shown = true
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
				m.finishUserPrompt()
				return m.advanceUserPrompts()
			}
		}
		m.userPrompts.permission = view
		m.syncApprovalToolState()
		m.userPrompts.approval = ask.NewPermission(req, m.conv.AgentToUI, view,
			input.BuildApprovalOptions)
		if m.userPrompts.approval == nil {
			m.finishUserPrompt()
			return m.advanceUserPrompts()
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
	if m.userPromptQueueContext() != "" {
		reserved++
	}
	m.userInput.Approval.Resize(max(1, m.env.Width-1), max(1, m.env.Height-reserved))
}

func (m *model) finishUserPrompt() {
	if req := m.userPrompts.active; req != nil {
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
	finished := m.userPrompts.active
	m.userPrompts.active = nil
	m.userPrompts.permission = nil
	m.userPrompts.approval = nil
	m.userPrompts.shown = false
	if finished != nil && finished == m.tasks.review {
		m.resumeTaskViewer()
	} else if finished != nil {
		m.refreshTaskViewer()
	}
}

func (m *model) closeUserPrompts() {
	m.hideTaskViewer()
	m.conv.AgentToUI.Close()
	cancelUserPrompt(m.userPrompts.active)
	m.userPrompts.queue.Cancel()
	m.finishUserPrompt()
}

func (m *model) resetUserPrompts() {
	m.closeUserPrompts()
	m.conv.AgentToUI = transcript.NewAgentToUI(100)
}
