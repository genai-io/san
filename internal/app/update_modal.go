// Operation-mode cycling (Shift+Tab) and the question-modal protocol used
// by AskUserQuestion-style prompts surfaced from tools.
package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/tool"
)

func (m *model) cycleOperationMode() {
	allowBypass := m.services.Setting != nil && m.services.Setting.AllowBypass()
	m.env.OperationMode = m.env.OperationMode.NextWithBypass(allowBypass)
	m.env.ApplyModePermissions(m.env.CWD)

	if m.services.Hook != nil {
		m.services.Hook.SetPermissionMode(m.env.OperationModeName())
	}
}

func (m *model) updateMode(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case conv.ProgressQuestionMsg:
		cmd := m.handleQuestionRequest(conv.QuestionRequestMsg{
			Request: msg.Request,
			Reply:   msg.Reply,
		})
		if m.conv.ProgressHub != nil {
			cmd = tea.Batch(cmd, m.conv.ProgressHub.Check())
		}
		return cmd, true
	case conv.QuestionRequestMsg:
		return m.handleQuestionRequest(msg), true
	}
	return nil, false
}

func (m *model) handleQuestionRequest(msg conv.QuestionRequestMsg) tea.Cmd {
	m.conv.Modal.PendingQuestion = msg.Request
	m.conv.Modal.PendingQuestionReply = msg.Reply
	m.conv.Modal.Question.Show(msg.Request, m.env.Width)
	return tea.Batch(m.CommitMessages()...)
}

func (m *model) handleQuestionResponse(msg conv.QuestionResponseMsg) tea.Cmd {
	reply := m.conv.Modal.PendingQuestionReply
	m.conv.Modal.PendingQuestionReply = nil
	defer func() { m.conv.Modal.PendingQuestion = nil }()

	if reply == nil {
		return nil
	}

	if msg.Cancelled {
		reply <- &tool.QuestionResponse{
			RequestID: msg.Request.ID,
			Cancelled: true,
		}
		return nil
	}
	reply <- msg.Response
	return nil
}
