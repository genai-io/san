package app

import (
	"context"
	"reflect"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
)

type flowMainStepInput struct {
	messageID string
	commit    func() tea.Cmd
}

// A native enqueue is not durable admission. Keep the editable head in place
// until the opened session projects this exact input, without a pending copy.
func (m *model) releaseFlowMainStepInput() (tea.Cmd, bool) {
	r := m.flowMainRequest
	if r == nil || !m.flowMainWriterActive() || r.msg.ctx.Err() != nil || m.pendingMessages.Held() {
		return nil, false
	}
	if r.step != nil {
		return m.confirmFlowMainStepInput(), false
	}
	head, ok := m.userInput.Queue.At(0)
	if !ok {
		return nil, false
	}
	head.Images = slices.Clone(head.Images)
	if blocked, reason := m.checkPromptHook(context.Background(), head.Content); blocked {
		m.pendingMessages.Hold(nil)
		m.conv.AddNotice("Queued Flow input was blocked: " + reason)
		return tea.Batch(m.CommitMessages()...), false
	}
	if !m.services.Agent.Active() || r.msg.lease.owner.Err() != nil {
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	content, fileImages, err := input.ProcessImageRefs(m.env.CWD, head.Content)
	if err != nil {
		m.conv.AddNotice("Image error: " + err.Error())
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	content = strings.TrimSpace(core.InlineImageTokenRe.ReplaceAllString(content, ""))
	display, images := input.ImageDisplay(m.env.CWD, head.Content, head.Images, fileImages)
	content = m.describeImages(content, images)
	chat := core.ChatMessage{ID: core.NewMessageID(), Role: core.ChatUser, Content: content, DisplayContent: display, Images: images}
	message, ok := chat.ToMessage()
	if !ok {
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	if err := m.startMemoryRecall(message.Text()); err != nil {
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	message = m.attachPendingReminders(message)
	if r != m.flowMainRequest || !m.flowMainWriterActive() || r.msg.ctx.Err() != nil {
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	if err := m.services.Agent.SendInput(pendingMessage{Message: message, FromUser: true}); err != nil {
		m.pendingMessages.Hold(nil)
		return nil, false
	}
	r.step = &flowMainStepInput{messageID: message.ID, commit: func() tea.Cmd {
		return m.commitFlowMainStepInput(r, head, chat)
	}}
	return m.confirmFlowMainStepInput(), true
}

func (m *model) commitFlowMainStepInput(r *flowMainRequest, sent input.QueueItem, chat core.ChatMessage) tea.Cmd {
	appendFlowMainInstructions(r.msg.lease, chat.Content)
	if current, ok := m.userInput.Queue.At(0); ok && reflect.DeepEqual(current, sent) {
		m.userInput.Queue.Dequeue()
	}
	for _, existing := range m.conv.Messages {
		if existing.ID == chat.ID {
			return tea.Batch(m.CommitMessages()...)
		}
	}
	m.conv.Append(chat)
	return tea.Batch(m.CommitMessages()...)
}

func (m *model) confirmFlowMainStepInput() tea.Cmd {
	r := m.flowMainRequest
	if r == nil || r.step == nil || !m.flowMainWriterActive() || r.msg.ctx.Err() != nil || m.pendingMessages.Held() {
		return nil
	}
	if r.msg.lease.owner.Err() != nil {
		m.pendingMessages.Hold(nil)
		return nil
	}
	snapshot := r.msg.lease.owner.Snapshot()
	for _, message := range snapshot.ActiveMessages() {
		if message.ID == r.step.messageID {
			commit := r.step.commit
			r.step = nil
			return commit()
		}
	}
	return nil
}
