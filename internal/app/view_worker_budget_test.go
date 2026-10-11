package app

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
)

func TestWorkerOverflowPreservesOrdinaryToolSibling(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		m := fixedComposerModel(80, 10)
		calls := append(reviewerCalls(3), core.ToolCall{
			ID: "mixed-shell", Name: "Bash",
			Input: `{"command":"printf MIXED_SHELL","description":"MIXED_SHELL"}`,
		})
		m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: calls, ToolCallsExpanded: expanded}}
		setReviewerActivity(m, calls)
		frame, cursor := m.viewString()
		plain := ansi.Strip(frame)
		if !strings.Contains(plain, "MIXED_SHELL") || rowCount(frame) != 10 || cursor.Position.Y != 7 {
			t.Fatalf("expanded=%v lost ordinary sibling or displaced composer:\n%s", expanded, plain)
		}
		for _, call := range calls[:3] {
			if !strings.Contains(plain, call.ID) {
				t.Fatalf("mixed batch hid worker %s:\n%s", call.ID, plain)
			}
		}
	}
}

func TestWorkflowIndicatorsCannotHideAllForegroundWorkers(t *testing.T) {
	task.ResetDefaultTracker()
	t.Cleanup(task.ResetDefaultTracker)
	for _, height := range []int{10, 24} {
		task.ResetDefaultTracker()
		m := fixedComposerModel(80, height)
		m.services.Task = task.Default()
		workflow := task.NewAgentTask("live-workflow", "workflow", "Run workflow", context.Background(), nil, "")
		workflow.SetLiveView(strings.Repeat("WORKFLOW_ACTIVITY\n", 40))
		m.services.Task.RegisterTask(workflow)
		calls := reviewerCalls(4)
		m.conv.Messages = []core.ChatMessage{
			{Role: core.ChatAssistant, ToolCalls: calls, ToolCallsExpanded: true},
			{Role: core.ChatNotice, Content: "WORKFLOW_ERROR_MARKER", AgentNotice: true},
		}
		setReviewerActivity(m, calls)
		frame, cursor := m.viewString()
		plain := ansi.Strip(frame)
		if !strings.Contains(plain, "WORKFLOW_ERROR_MARKER") {
			t.Errorf("workflow hid the latest error notice:\n%s", plain)
		}
		for _, call := range calls {
			if !strings.Contains(plain, call.ID) {
				t.Errorf("height=%d workflow hid %s:\n%s", height, call.ID, plain)
			}
		}
		if rowCount(frame) != height || cursor.Position.Y != height-3 {
			t.Fatalf("workflow displaced composer:\n%s", plain)
		}
	}
}
