package conv

import (
	"testing"

	"github.com/genai-io/gen-code/internal/core"
)

func TestHandleProgressWithoutHubDoesNotPanic(t *testing.T) {
	m := OutputModel{Spinner: newSpinner(), MDRenderer: NewMDRenderer(80)}

	cmd := m.HandleProgress(ProgressUpdateMsg{
		Index:   1,
		Message: "step",
	})
	if cmd == nil {
		t.Fatal("expected spinner cmd even without progress hub")
	}
	if len(m.TaskProgress[1]) != 1 || m.TaskProgress[1][0] != "step" {
		t.Fatalf("unexpected progress state: %#v", m.TaskProgress)
	}
}

func Test_drainProgressWithoutHubIsNoop(t *testing.T) {
	m := OutputModel{Spinner: newSpinner(), MDRenderer: NewMDRenderer(80)}
	m.TaskProgress = map[int][]string{2: {"existing"}}

	m.drainProgress()

	if len(m.TaskProgress[2]) != 1 || m.TaskProgress[2][0] != "existing" {
		t.Fatalf("unexpected progress state after drain: %#v", m.TaskProgress)
	}
}

func TestMarkToolCallCompleteAdvancesAndClearsPendingState(t *testing.T) {
	state := ToolExecState{}
	state.Track([]core.ToolCall{
		{ID: "tc-1", Name: "WebFetch"},
		{ID: "tc-2", Name: "Grep"},
	})

	state.MarkCurrent("tc-1")
	if state.CurrentIdx != 0 {
		t.Fatalf("CurrentIdx = %d, want 0", state.CurrentIdx)
	}

	state.MarkComplete("tc-1")
	if state.CurrentIdx != 1 {
		t.Fatalf("CurrentIdx = %d, want 1", state.CurrentIdx)
	}
	if len(state.PendingCalls) != 2 {
		t.Fatalf("PendingCalls length = %d, want 2", len(state.PendingCalls))
	}

	state.MarkComplete("tc-2")
	if state.PendingCalls != nil {
		t.Fatalf("PendingCalls = %#v, want nil", state.PendingCalls)
	}
	if state.CurrentIdx != 0 {
		t.Fatalf("CurrentIdx = %d, want 0", state.CurrentIdx)
	}
}

func TestAppendInterruptedByUserMarkerAddsMessage(t *testing.T) {
	m := NewConversation()
	m.Append(core.ChatMessage{Role: core.RoleAssistant, Content: "partial [Interrupted]"})

	m.AppendInterruptedByUserMarker()

	if len(m.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.Messages))
	}
	last := m.Messages[len(m.Messages)-1]
	if last.Role != core.RoleUser || last.Content != InterruptedByUserMarker {
		t.Fatalf("unexpected last message: %#v", last)
	}
}

func TestAppendInterruptedByUserMarkerIsIdempotent(t *testing.T) {
	m := NewConversation()
	m.Append(core.ChatMessage{Role: core.RoleAssistant, Content: "x"})
	m.AppendInterruptedByUserMarker()
	m.AppendInterruptedByUserMarker()

	if len(m.Messages) != 2 {
		t.Fatalf("expected idempotent append, got %d messages: %#v", len(m.Messages), m.Messages)
	}
}

func TestAppendInterruptedByUserMarkerOnEmptyConversation(t *testing.T) {
	m := NewConversation()

	m.AppendInterruptedByUserMarker()

	if len(m.Messages) != 1 {
		t.Fatalf("expected marker to be appended on empty conv, got %d", len(m.Messages))
	}
	if m.Messages[0].Content != InterruptedByUserMarker {
		t.Fatalf("unexpected content: %q", m.Messages[0].Content)
	}
}
