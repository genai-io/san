package tui

import (
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
)

func TestTUIMemoryLanguageReachesMain(t *testing.T) {
	for _, text := range []string{"remember: Use make.", "update memory: Use ninja.", "forget: Use make.", "请记住：优先使用表格测试。"} {
		t.Run(text, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.userInput.Textarea.SetValue(text)
			m.handleSubmit()
			if m.memoryInput != nil {
				t.Fatal("default recall should not prepare this input")
			}
			assertMainReceivedText(t, provider.requests, text)
		})
	}
}

func TestFlowMemoryLanguageReachesMain(t *testing.T) {
	for _, text := range []string{"forget: Use make.", "请记住：优先使用表格测试。"} {
		t.Run(text, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.flowMain = newFlowMainBridge()
			owner := m.services.Session
			executor, err := m.flowMain.bind(owner).Open(t.Context(), flow.MainRunInfo{SessionID: owner.ID()})
			if err != nil {
				t.Fatal(err)
			}
			lease := executor.(*flowMainLease)
			m.flowMainRequest = &flowMainRequest{msg: flowMainMsg{lease: lease, ctx: t.Context()}, messageID: "writer", outbox: m.AgentOutbox()}
			m.userInput.Queue.Enqueue(text, nil)
			if _, ok := m.releaseFlowMainStepInput(); !ok || m.memoryInput != nil {
				t.Fatal("Flow input was not sent directly to main")
			}
			assertMainReceivedText(t, provider.requests, text)
		})
	}
}

func assertMainReceivedText(t *testing.T, requests <-chan []core.Message, text string) {
	t.Helper()
	select {
	case messages := <-requests:
		for _, message := range messages {
			if message.Text() == text {
				return
			}
		}
		t.Fatalf("main request did not contain %q: %+v", text, messages)
	case <-time.After(3 * time.Second):
		t.Fatal("main did not receive input")
	}
}

func TestCancelledMemoryRecallDoesNotAdmitLateResult(t *testing.T) {
	m, provider := compactQueueModel(t)
	head := pendingMessage{Message: core.UserMessage("How do we build?", nil), FromUser: true}
	m.pendingMessages.Enqueue(head)
	m.prepareMemoryInput(head)
	request := m.memoryInput
	m.handleStreamCancel()
	cmd := m.onMemoryInput(memoryInputMsg{input: request, result: agent.PreparedMemory{Message: head.Message}})
	if cmd != nil || m.memoryInput != nil || !m.pendingMessages.Held() || m.pendingMessages.Len() != 1 || m.pendingMessages.First().MemoryReady {
		t.Fatal("late recall result changed the held queue")
	}
	select {
	case <-provider.requests:
		t.Fatal("cancelled recall submitted a main request")
	default:
	}
}

func TestMemoryRecallCarriesWaitIntoSubmission(t *testing.T) {
	m, _ := compactQueueModel(t)
	head := pendingMessage{Message: core.UserMessage("How do we build?", nil), FromUser: true}
	m.pendingMessages.Enqueue(head)
	m.prepareMemoryInput(head)
	request := m.memoryInput
	m.onMemoryInput(memoryInputMsg{input: request, result: agent.PreparedMemory{Message: head.Message}})
	if m.memoryInput != nil || !m.conv.Stream.Waiting || m.conv.Stream.StartedAt != request.startedAt {
		t.Fatal("recall did not hand the wait clock to the main turn")
	}
}
