package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
)

func flowMainStepModel(t *testing.T) *model {
	t.Helper()
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	executor := m.flowMain.bind(m.features.Session)
	lease, err := executor.Open(t.Context(), flow.MainRunInfo{SessionID: m.features.Session.ID()})
	if err != nil {
		t.Fatal(err)
	}
	m.conv.Stream.Active = true
	m.flowMainRequest = &flowMainRequest{msg: flowMainMsg{lease: lease.(*flowMainLease), ctx: t.Context(), call: &flow.Call{}, reply: make(chan flowMainReply, 1)}, messageID: "owned-writer", outbox: m.AgentOutbox()}
	return m
}

func TestRetiredFlowInputStillUsesPromptHook(t *testing.T) {
	m := flowMainStepModel(t)
	id := m.userInput.Queue.Enqueue("retained Flow amendment", nil)
	m.userInput.Queue.RequirePromptAdmission(id)
	m.features.Hook = modelWithPromptHook(t, "exit 2").features.Hook
	m.flowMain.bind(nil)
	if _, released := m.releaseQueuedMessage(); released || m.userInput.Queue.Len() != 1 || !m.pendingMessages.Held() {
		t.Fatal("retiring a Flow allowed its retained input to bypass prompt admission")
	}
}

func TestFlowMainStepAdmissionFailureKeepsQueue(t *testing.T) {
	for _, mode := range []string{"inactive", "recording-failed", "hook-blocked"} {
		t.Run(mode, func(t *testing.T) {
			m := flowMainStepModel(t)
			lease := m.flowMainRequest.msg.lease
			m.userInput.Queue.Enqueue("safe-step amendment", nil)
			switch mode {
			case "inactive":
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if err := m.features.Agent.Stop(ctx); err != nil {
					t.Fatal(err)
				}
				m.flowMainRequest.outbox = m.AgentOutbox()
			case "recording-failed":
				if err := m.features.Session.Close(); err != nil {
					t.Fatal(err)
				}
			case "hook-blocked":
				m.features.Hook = modelWithPromptHook(t, "exit 2").features.Hook
			}
			if _, released := m.releaseQueuedMessage(); released {
				t.Fatal("failed native admission consumed queue")
			}
			if m.userInput.Queue.Len() != 1 || lease.Instructions() != "" || m.pendingMessages.Len() != 0 || !m.pendingMessages.Held() {
				t.Fatal("failed safe-step lost queue, polluted critic, or left a pending copy")
			}
			m.releaseQueuedMessage()
			if m.userInput.Queue.Len() != 1 || lease.Instructions() != "" {
				t.Fatal("held safe-step retried")
			}
		})
	}
}

func TestFlowMainStepAcceptedBeforeQueueEdit(t *testing.T) {
	m := flowMainStepModel(t)
	m.userInput.Queue.Enqueue("accepted direction", nil)
	sent, _ := m.userInput.Queue.At(0)
	m.userInput.Queue.UpdateAt(0, "next revision direction", nil)
	chat := core.ChatMessage{ID: "accepted-input", Role: core.ChatUser, Content: sent.Content}
	m.acceptFlowMainInput(m.flowMainRequest.msg.lease, []input.QueueItem{sent}, chat.Content, &chat, false)()
	if m.flowMainRequest.msg.lease.Instructions() != sent.Content {
		t.Fatal("an accepted direction disappeared from the critic snapshot after editing")
	}
	queued, ok := m.userInput.Queue.At(0)
	if !ok || queued.Content != "next revision direction" || m.pendingMessages.Held() {
		t.Fatal("concurrent edit was lost or prevented the next writer revision")
	}
}

func TestFlowMainInputConfirmationRequiresExactID(t *testing.T) {
	m := flowMainStepModel(t)
	commits := 0
	r := m.flowMainRequest
	r.input = &flowMainInput{messageID: "not-in-owner-snapshot", commit: func() tea.Cmd {
		commits++
		return nil
	}}
	m.confirmFlowMainInput()
	if commits != 0 || r.input == nil {
		t.Fatal("unprojected receipt was committed")
	}
}

func TestFlowMainInputAcceptedQueueMutation(t *testing.T) {
	for _, mutation := range []string{"edit", "delete", "reorder"} {
		t.Run(mutation, func(t *testing.T) {
			m := flowMainStepModel(t)
			m.userInput.Queue.Enqueue("accepted", nil)
			m.userInput.Queue.Enqueue("later", nil)
			sent, _ := m.userInput.Queue.At(0)
			if mutation == "reorder" {
				sent, _ = m.userInput.Queue.At(1)
			}
			chat := core.ChatMessage{ID: "accepted-input", Role: core.ChatUser, Content: sent.Content}
			commit := m.acceptFlowMainInput(m.flowMainRequest.msg.lease, []input.QueueItem{sent}, chat.Content, &chat, false)
			switch mutation {
			case "edit":
				m.userInput.Queue.UpdateAt(0, "edited", nil)
			case "delete":
				m.userInput.Queue.Dequeue()
			case "reorder":
				m.userInput.Queue.Dequeue()
				m.userInput.Queue.Enqueue("replacement", nil)
			}
			commit()
			commit()
			if got := m.flowMainRequest.msg.lease.Instructions(); got != sent.Content {
				t.Fatalf("accepted instructions = %q", got)
			}
			count := 0
			for _, message := range m.conv.Messages {
				if message.ID == chat.ID {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("accepted display count = %d", count)
			}
			want := 1
			if mutation == "edit" {
				want = 2
			}
			if got := m.userInput.Queue.Len(); got != want {
				t.Fatalf("queue length = %d, want %d", got, want)
			}
		})
	}
}

func TestFlowMainStepAdmissionExactlyOnce(t *testing.T) {
	m := flowMainStepModel(t)
	lease := m.flowMainRequest.msg.lease
	const amendment = "safe-step accepted once"
	m.userInput.Queue.Enqueue(amendment, nil)
	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("native safe-step not enqueued")
	}
	if m.pendingMessages.Len() != 0 {
		t.Fatal("safe-step duplicated into pending outbox")
	}
	// The owner may project the input before the initial send returns.
	if m.userInput.Queue.Len() > 0 && m.flowMainRequest.input == nil {
		t.Fatal("queued safe-step is missing its native admission receipt")
	}
	if _, released := m.releaseQueuedMessage(); released {
		t.Fatal("repeated boundary released the same input")
	}
	deadline := time.After(3 * time.Second)
	for m.userInput.Queue.Len() > 0 {
		select {
		case event := <-m.AgentOutbox():
			m.confirmFlowMainInput()
			chat.Update(m, &m.conv, chat.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: event})
		case <-deadline:
			t.Fatal("safe-step input never admitted")
		}
	}
	if m.flowMainRequest.input != nil {
		t.Fatal("confirmed safe-step left a pending receipt")
	}
	if strings.Count(lease.Instructions(), amendment) != 1 {
		t.Fatalf("critic instructions = %q", lease.Instructions())
	}
	count := 0
	var id string
	for _, message := range m.features.Agent.Messages() {
		if strings.Contains(message.Text(), amendment) {
			count++
			id = message.ID
		}
	}
	if count != 1 || id == "" {
		t.Fatalf("native message admitted %d times (ID %q)", count, id)
	}
	count = 0
	for _, message := range m.conv.Messages {
		if message.ID == id && message.Content == amendment {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("admitted message displayed %d times", count)
	}
	if m.pendingMessages.Len() != 0 {
		t.Fatal("native safe-step left pending duplicate")
	}
}
