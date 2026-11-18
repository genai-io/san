package app

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
)

type flowMainInput struct {
	messageID string
	commit    func() tea.Cmd
}

// flowMainWriterInput admits queued amendments with the writer's first input,
// including writers that finish without a tool/step boundary. The returned
// closure runs on the UI goroutine only after native input admission succeeds;
// checkpoint/admission failure leaves the queued amendments untouched. Only
// accepted amendments enter the lease snapshot used by the next critic.
func (m *model) flowMainWriterInput(prompt string) (core.Message, tea.Cmd, func() tea.Cmd, error) {
	if blocked, reason := m.checkPromptHook(context.Background(), prompt); blocked {
		return core.Message{}, nil, nil, fmt.Errorf("Flow writer prompt blocked: %s", reason)
	}
	if m.userInput.Queue.SelectIdx == 0 {
		return core.Message{}, nil, nil, fmt.Errorf("queued writer input is still being edited")
	}
	draft := m.userInput.Textarea.Value()
	images := slices.Clone(m.userInput.Images.Pending)
	nextID := m.userInput.Images.NextID
	defer func() {
		m.userInput.Textarea.SetValue(draft)
		m.userInput.Images.Pending, m.userInput.Images.NextID = images, nextID
	}()

	var amendments []string
	var prepared []input.QueueItem
	var attachments []core.Attachment
	for _, item := range m.flowMainAmendments() {
		if blocked, reason := m.checkPromptHook(context.Background(), item.Content); blocked {
			return core.Message{}, nil, nil, fmt.Errorf("queued writer input blocked: %s", reason)
		}
		m.userInput.RestoreQueuedImages(item.Content, item.Images)
		chat, ok := m.buildUserMessage(item.Content)
		if !ok {
			return core.Message{}, nil, nil, fmt.Errorf("queued writer attachment could not be prepared")
		}
		amendments = append(amendments, m.describeImages(chat.Content, chat.Images))
		prepared = append(prepared, item)
		attachments = append(attachments, chat.Images...)
	}
	if len(amendments) > 0 {
		prompt += "\n\nUser amendments for this revision:\n\n" + strings.Join(amendments, "\n\n")
	}
	var start tea.Cmd
	if !m.services.Agent.Active() {
		if m.env.LLMProvider == nil {
			return core.Message{}, nil, nil, fmt.Errorf("main writer has no provider configured")
		}
		var err error
		start, err = m.ensureAgentRuntime()
		if err != nil {
			return core.Message{}, nil, nil, err
		}
	}
	display := "Flow step"
	if r := m.flowMainRequest; r != nil && r.msg.call != nil && r.msg.call.Node != nil {
		display += " · " + r.msg.call.Node.ID
	}
	if len(amendments) > 0 {
		display += "\n\n" + strings.Join(amendments, "\n\n")
	}
	chat := m.conv.Append(core.ChatMessage{Role: core.ChatUser, Content: prompt, DisplayContent: display, Images: attachments})
	message, ok := chat.ToMessage()
	if !ok {
		return core.Message{}, nil, nil, fmt.Errorf("Flow writer input could not be prepared")
	}
	return message, start, m.acceptFlowMainInput(m.flowMainLease(), prepared, strings.Join(amendments, "\n\n"), nil, true), nil
}

// flowMainAmendments is the queued prefix a writer revision consumes; a
// command or exit request ends it and runs after Flow releases input.
func (m *model) flowMainAmendments() []input.QueueItem {
	items := m.userInput.Queue.Items()
	for i, item := range items {
		if m.isSlashCommand(item.Content) || input.IsExitRequest(item.Content) {
			return items[:i]
		}
	}
	return items
}

// acceptFlowMainInput captures accepted text independently of later queue edits.
// Only unchanged entries are removed; a receipt can be committed only once.
func (m *model) acceptFlowMainInput(lease *flowMainLease, prepared []input.QueueItem, text string, chat *core.ChatMessage, record bool) func() tea.Cmd {
	admitted := false
	return func() tea.Cmd {
		if admitted {
			return nil
		}
		admitted = true
		appendFlowMainInstructions(lease, text)
		for _, sent := range prepared {
			for i, current := range m.userInput.Queue.Items() {
				if current.ID == sent.ID {
					if reflect.DeepEqual(current, sent) {
						m.userInput.Queue.UpdateAt(i, "", nil)
						if record {
							m.userInput.RecordSubmission(m.env.CWD, sent.Content)
						}
					}
					break
				}
			}
		}
		if chat != nil {
			found := slices.ContainsFunc(m.conv.Messages, func(existing core.ChatMessage) bool { return existing.ID == chat.ID })
			if !found {
				m.conv.Append(*chat)
			}
		}
		return tea.Batch(m.CommitMessages()...)
	}
}

// A native enqueue is not durable admission. Retain the editable queue until
// the opened owner projects the exact input ID, without a pending outbox copy.
func (m *model) releaseFlowMainStepInput() (tea.Cmd, bool) {
	r := m.flowMainRequest
	if r == nil || !m.flowMainWriterActive() || r.msg.ctx.Err() != nil || m.pendingMessages.Held() {
		return nil, false
	}
	if r.input != nil {
		return m.confirmFlowMainInput(), false
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
	chat := core.ChatMessage{ID: core.NewMessageID(), Role: core.ChatUser, Content: m.describeImages(content, images), DisplayContent: display, Images: images}
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
	r.input = &flowMainInput{messageID: message.ID, commit: m.acceptFlowMainInput(r.msg.lease, []input.QueueItem{head}, chat.Content, &chat, false)}
	return m.confirmFlowMainInput(), true
}

func (m *model) confirmFlowMainInput() tea.Cmd {
	r := m.flowMainRequest
	if r == nil || r.input == nil || !m.flowMainWriterActive() || r.msg.ctx.Err() != nil || m.pendingMessages.Held() {
		return nil
	}
	if r.msg.lease.owner.Err() != nil {
		m.pendingMessages.Hold(nil)
		return nil
	}
	snapshot := r.msg.lease.owner.Snapshot()
	for _, message := range snapshot.ActiveMessages() {
		if message.ID == r.input.messageID {
			commit := r.input.commit
			r.input = nil
			return commit()
		}
	}
	return nil
}

func appendFlowMainInstructions(lease *flowMainLease, text string) {
	if lease == nil || strings.TrimSpace(text) == "" {
		return
	}
	lease.bridge.mu.Lock()
	defer lease.bridge.mu.Unlock()
	if lease.bridge.lease == lease {
		lease.instructions = strings.TrimSpace(lease.instructions + "\n\n" + text)
	}
}
