package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
)

func flowMainStepModel(t *testing.T) *model {
	t.Helper()
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	executor := m.flowMain.bind(m.services.Session)
	lease, err := executor.Open(t.Context(), flow.MainRunInfo{SessionID: m.services.Session.ID()})
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
	m.services.Hook = modelWithPromptHook(t, "exit 2").services.Hook
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
				if err := m.services.Agent.Stop(ctx); err != nil {
					t.Fatal(err)
				}
				m.flowMainRequest.outbox = m.AgentOutbox()
			case "recording-failed":
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
			case "hook-blocked":
				m.services.Hook = modelWithPromptHook(t, "exit 2").services.Hook
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
	m.commitFlowMainStepInput(m.flowMainRequest, sent, chat)
	if m.flowMainRequest.msg.lease.Instructions() != sent.Content {
		t.Fatal("an accepted direction disappeared from the critic snapshot after editing")
	}
	queued, ok := m.userInput.Queue.At(0)
	if !ok || queued.Content != "next revision direction" || m.pendingMessages.Held() {
		t.Fatal("concurrent edit was lost or prevented the next writer revision")
	}
}

func TestFlowMainStepAdmissionExactlyOnce(t *testing.T) {
	m := flowMainStepModel(t)
	lease := m.flowMainRequest.msg.lease
	m.userInput.Queue.Enqueue("safe-step accepted once", nil)
	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("native safe-step not enqueued")
	}
	if m.pendingMessages.Len() != 0 {
		t.Fatal("safe-step duplicated into pending outbox")
	}
	if m.flowMainRequest.step == nil {
		t.Fatal("missing native admission receipt")
	}
	id := m.flowMainRequest.step.messageID
	// Repeated boundaries cannot send a second input while projection is pending.
	m.releaseQueuedMessage()
	deadline := time.After(3 * time.Second)
	for m.userInput.Queue.Len() > 0 {
		select {
		case event := <-m.AgentOutbox():
			m.confirmFlowMainStepInput()
			conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: event})
		case <-deadline:
			t.Fatal("safe-step input never admitted")
		}
	}
	if strings.Count(lease.Instructions(), "safe-step accepted once") != 1 {
		t.Fatalf("critic instructions = %q", lease.Instructions())
	}
	count := 0
	for _, message := range m.services.Agent.Messages() {
		if message.ID == id {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("native message admitted %d times", count)
	}
	if m.pendingMessages.Len() != 0 {
		t.Fatal("native safe-step left pending duplicate")
	}
}
