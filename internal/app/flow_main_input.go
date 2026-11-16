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
	for _, item := range m.userInput.Queue.Items() {
		if m.isSlashCommand(item.Content) || input.IsExitRequest(item.Content) {
			break
		}
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
	lease := m.flowMainLease()
	admitted := false
	admit := func() tea.Cmd {
		if admitted {
			return nil
		}
		admitted = true
		appendFlowMainInstructions(lease, strings.Join(amendments, "\n\n"))
		for _, sent := range prepared {
			for i, current := range m.userInput.Queue.Items() {
				if current.ID != sent.ID {
					continue
				}
				if reflect.DeepEqual(current, sent) {
					m.userInput.Queue.UpdateAt(i, "", nil)
					m.userInput.RecordSubmission(m.env.CWD, sent.Content)
				}
				break
			}
		}
		return tea.Batch(m.CommitMessages()...)
	}
	return message, start, admit, nil
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
