package conv

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestHandleProgressWithoutAgentToUIDoesNotPanic(t *testing.T) {
	m := OutputModel{Spinner: newFrameClock(), MDRenderer: NewMDRenderer(80)}

	cmd := m.HandleProgress(AgentStatusMsg{
		Index:   1,
		Message: "step",
	})
	if cmd == nil {
		t.Fatal("expected spinner cmd even without an agent-to-UI channel")
	}
	if len(m.TaskProgress[1]) != 1 || m.TaskProgress[1][0] != "step" {
		t.Fatalf("unexpected progress state: %#v", m.TaskProgress)
	}
}

func Test_drainProgressWithoutHubIsNoop(t *testing.T) {
	m := OutputModel{Spinner: newFrameClock(), MDRenderer: NewMDRenderer(80)}
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
