package conv

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestHandleActivityWithoutAgentToUIDoesNotPanic(t *testing.T) {
	m := OutputModel{Spinner: newFrameClock(), MDRenderer: NewMDRenderer(80)}

	cmd := m.HandleActivity(AgentActivityMsg{
		Index:   1,
		Message: "step",
	})
	if cmd == nil {
		t.Fatal("expected spinner cmd even without an agent-to-UI channel")
	}
	if len(m.TaskActivity[1]) != 1 || m.TaskActivity[1][0] != "step" {
		t.Fatalf("unexpected activity state: %#v", m.TaskActivity)
	}
}

func Test_drainProgressWithoutHubIsNoop(t *testing.T) {
	m := OutputModel{Spinner: newFrameClock(), MDRenderer: NewMDRenderer(80)}
	m.TaskActivity = map[int][]string{2: {"existing"}}

	m.drainProgress()

	if len(m.TaskActivity[2]) != 1 || m.TaskActivity[2][0] != "existing" {
		t.Fatalf("unexpected activity state after drain: %#v", m.TaskActivity)
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
