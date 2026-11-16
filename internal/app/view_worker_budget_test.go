package app

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestFlowFooterSuppressesOnlyGraphManagedTrackerRows(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		mgr := task.NewManager()
		tracker := todo.NewStore()
		ctx := context.Background()
		flow := mgr.CreateSchedulerTask("footer-flow", "flow", "DUPLICATE_SCHEDULER", ctx, nil)
		flow.SetLiveView("    Internal Flow Read Only Review · 0/2 steps\n    map ◉ → review ○")
		child := mgr.CreateChildAgentTask("footer-child", flow.GetID(), "Reviewer", "DUPLICATE_CHILD", ctx, nil)
		todo.TrackWorker(tracker, flow.GetStatus())
		todo.TrackWorker(tracker, child.GetStatus())
		if mixed {
			ordinary := mgr.CreateAgentTask("ordinary", "Reviewer", "ORDINARY_WORK", ctx, nil)
			todo.TrackWorker(tracker, ordinary.GetStatus())
			// A child of a scheduler without a visible graph is not redundant.
			other := mgr.CreateSchedulerTask("other-flow", "flow", "OTHER_SCHEDULER", ctx, nil)
			uncovered := mgr.CreateChildAgentTask("uncovered", other.GetID(), "Reviewer", "UNCOVERED_CHILD", ctx, nil)
			todo.TrackWorker(tracker, uncovered.GetStatus())
		}
		m := fixedComposerModel(80, 24)
		m.services.Task, m.services.Tracker = mgr, tracker
		m.conv.ShowTasks = true
		view := ansi.Strip(m.renderTrackerList())
		if strings.Contains(view, "DUPLICATE_") || !strings.Contains(view, "Internal Flow Read Only Review") {
			t.Fatalf("mixed=%v duplicate rows or missing graph:\n%s", mixed, view)
		}
		if mixed {
			for _, label := range []string{"Tasks", "ORDINARY_WORK", "UNCOVERED_CHILD"} {
				if !strings.Contains(view, label) {
					t.Errorf("missing ordinary task content %q:\n%s", label, view)
				}
			}
		} else if strings.Contains(view, "Tasks") {
			t.Fatalf("graph-only footer has redundant caption:\n%s", view)
		}
		wantItems := 2
		if mixed {
			wantItems = 4
		}
		if len(tracker.List()) != wantItems {
			t.Fatal("rendering changed tracker storage")
		}
	}
}

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

func TestFlowIndicatorsCannotHideAllForegroundWorkers(t *testing.T) {
	task.ResetDefaultTracker()
	t.Cleanup(task.ResetDefaultTracker)
	for _, height := range []int{10, 24} {
		task.ResetDefaultTracker()
		m := fixedComposerModel(80, height)
		m.services.Task = task.Default()
		flow := task.NewAgentTask("live-flow", "flow", "Run flow", context.Background(), nil, "")
		flow.MarkScheduler()
		flow.SetLiveView(strings.Repeat("WORKFLOW_ACTIVITY\n", 40))
		m.services.Task.RegisterTask(flow)
		calls := reviewerCalls(4)
		m.conv.Messages = []core.ChatMessage{
			{Role: core.ChatAssistant, ToolCalls: calls, ToolCallsExpanded: true},
			{Role: core.ChatNotice, Content: "WORKFLOW_ERROR_MARKER", AgentNotice: true},
		}
		setReviewerActivity(m, calls)
		frame, cursor := m.viewString()
		plain := ansi.Strip(frame)
		if !strings.Contains(plain, "WORKFLOW_ERROR_MARKER") {
			t.Errorf("flow hid the latest error notice:\n%s", plain)
		}
		for _, call := range calls {
			if !strings.Contains(plain, call.ID) {
				t.Errorf("height=%d flow hid %s:\n%s", height, call.ID, plain)
			}
		}
		if rowCount(frame) != height || cursor.Position.Y != height-3 {
			t.Fatalf("flow displaced composer:\n%s", plain)
		}
	}
}
