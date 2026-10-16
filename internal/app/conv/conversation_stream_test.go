package conv

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestStreamMetadataSkipsNotices(t *testing.T) {
	m := NewConversation()
	owner := m.Append(core.ChatMessage{Role: core.ChatAssistant})
	m.Stream = StreamState{Active: true, MessageID: owner.ID}
	m.AddNotice("background error")
	m.AddAgentNotice("another task completed")
	if m.StreamMessageIndex() != 0 {
		t.Fatal("notices hid the streaming assistant")
	}
	m.AppendStream("text", "reasoning")
	m.SetStreamToolCalls([]core.ToolCall{{ID: "read-1", Name: "Read"}})
	m.Stream.Active = false // Done arrives before InferenceEnd supplies the signature.
	m.SetStreamThinkingSignature("signature")
	got := m.Messages[0]
	if got.Content != "text" || got.Thinking != "reasoning" || len(got.ToolCalls) != 1 || got.ThinkingSignature != "signature" {
		t.Fatalf("stream metadata was lost: %+v", got)
	}
	if m.Messages[1].Content != "background error" || m.Messages[2].Content != "another task completed" {
		t.Fatal("stream updates corrupted notices")
	}
}

func TestStreamWritesToItsMessageIdentity(t *testing.T) {
	m := NewConversation()
	owner := m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "owner"})
	m.Stream = StreamState{Active: true, MessageID: owner.ID}
	m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "another row"})
	m.AddNotice("notice")
	// Bind the inference to the original row; display order cannot retarget it.
	m.AppendStream(" delta", "")
	if m.Messages[0].Content != "owner delta" || m.Messages[1].Content != "another row" {
		t.Fatalf("display order changed the stream target: %+v", m.Messages)
	}
}

func TestCanceledStreamRejectsLateMetadata(t *testing.T) {
	for _, boundary := range []core.ChatMessage{
		{Role: core.ChatUser, Content: "interrupted"},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "read-1"}},
	} {
		m := NewConversation()
		owner := m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "original"})
		m.Stream = StreamState{Active: true, MessageID: owner.ID}
		m.Stream.Stop()
		m.Append(boundary)
		m.AddNotice("background error")
		m.AppendStream("late delta", "late reasoning")
		m.SetStreamToolCalls([]core.ToolCall{{ID: "late"}})
		m.SetStreamThinkingSignature("late signature")
		got := m.Messages[0]
		if m.StreamMessageIndex() != -1 || got.Content != "original" || got.Thinking != "" || len(got.ToolCalls) != 0 || got.ThinkingSignature != "" {
			t.Fatalf("late stream event changed an earlier turn: %+v", got)
		}
	}
}

func TestRetryDropsAssistantAndPreservesNotices(t *testing.T) {
	m := NewConversation()
	m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "old turn"})
	m.CommittedCount = 1
	owner := m.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "partial reply"})
	m.Stream = StreamState{Active: true, MessageID: owner.ID}
	m.AddNotice("background error")
	m.DropStreamingAssistant()
	if len(m.Messages) != 2 || m.Messages[0].Content != "old turn" || m.Messages[1].Content != "background error" || m.CommittedCount != 1 {
		t.Fatalf("retry removed the wrong row: %+v", m)
	}
	m.DropStreamingAssistant()
	if len(m.Messages) != 2 {
		t.Fatal("retry removed committed history")
	}
}
