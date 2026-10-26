package input

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
)

func taskKey(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	if s == "esc" {
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	if s == "tab" {
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}
func TestTaskViewerSelectionRefreshAndScroll(t *testing.T) {
	var v TaskViewer
	a := TaskViewItem{ID: "a", Status: "running", Output: strings.Repeat("line\n", 70)}
	b := TaskViewItem{ID: "b", Status: "running"}
	v.Show([]TaskViewItem{a, b}, 80, 20)
	v.HandleKeypress(taskKey("enter"))
	v.body.ScrollDown(12)
	offset := v.body.YOffset()
	v.Refresh([]TaskViewItem{b, a})
	if v.selected != "a" || v.body.YOffset() != offset {
		t.Fatal("refresh moved selection or output scroll")
	}
	a.Status = "completed"
	v.Refresh([]TaskViewItem{a, b})
	v.body.GotoTop()
	if !strings.Contains(v.Render(), "completed") {
		t.Fatal("completion not refreshed")
	}
	v.Refresh([]TaskViewItem{b})
	if v.selected != "a" || !strings.Contains(v.Render(), "last snapshot") {
		t.Fatal("disappearing selection lost")
	}
	if cmd := v.HandleKeypress(taskKey("s")); cmd != nil || v.confirm {
		t.Fatal("obsolete selection actionable")
	}
}
func TestTaskViewerStopConfirmation(t *testing.T) {
	var v TaskViewer
	items := []TaskViewItem{{ID: "a", Status: "running"}, {ID: "b", Status: "running"}}
	v.Show(items, 80, 20)
	v.HandleKeypress(taskKey("s"))
	if !v.confirm || v.confirmStop {
		t.Fatal("confirmation must default Back")
	}
	v.HandleKeypress(taskKey("enter"))
	if !v.active || v.confirm {
		t.Fatal("Back must not stop")
	}
	v.HandleKeypress(taskKey("s"))
	v.HandleKeypress(taskKey("tab"))
	v.Refresh([]TaskViewItem{items[1], items[0]})
	cmd := v.HandleKeypress(taskKey("enter"))
	if cmd == nil || cmd().(TaskStopMsg).TaskID != "a" {
		t.Fatal("wrong stop identity")
	}
	for _, status := range []task.TaskStatus{task.StatusCompleted, task.StatusStopped, task.StatusFailed} {
		v.Show(items, 80, 20)
		v.HandleKeypress(taskKey("s"))
		v.HandleKeypress(taskKey("tab"))
		changed := items[0]
		changed.Status = status
		v.Refresh([]TaskViewItem{changed, items[1]})
		if v.confirm {
			t.Fatalf("%s did not cancel confirmation", status)
		}
	}
}
func TestTaskViewerActionIdentityAndResume(t *testing.T) {
	var v TaskViewer
	items := []TaskViewItem{{ID: "exact", Status: "running", AwaitingApproval: true, Output: strings.Repeat("line\n", 70)}}
	v.Show(items, 40, 20)
	v.HandleKeypress(taskKey("enter"))
	v.body.ScrollDown(10)
	offset, viewID := v.body.YOffset(), v.ViewID()
	cmd := v.HandleKeypress(taskKey("r"))
	if cmd == nil || cmd().(TaskReviewApprovalMsg).ViewID != viewID || !v.ActionPending() || v.IsActive() {
		t.Fatal("review must suspend with scoped identity and wait for root acknowledgement")
	}
	v.Suspend()
	v.Resume(items, 40, 20)
	if v.ViewID() == viewID || !v.detail || v.selected != "exact" || v.body.YOffset() != offset {
		t.Fatal("return lost identity, detail, or scroll; old action must also expire")
	}
	viewID = v.ViewID()
	v.Hide()
	if v.IsActive() || v.ActionPending() || v.ViewID() == viewID || v.archived != nil {
		t.Fatal("session teardown retained task state")
	}
}

func TestTaskViewerNarrowActionsAndWrappedSelection(t *testing.T) {
	for _, width := range []int{32, 40} {
		var v TaskViewer
		var items []TaskViewItem
		for i := range 8 {
			items = append(items, TaskViewItem{ID: fmt.Sprint(i), AgentName: "reviewer", Subject: strings.Repeat("review ", 12), Status: "running"})
		}
		v.Show(items, width, 12)
		for range 7 {
			v.HandleKeypress(taskKey("j"))
		}
		plain := ansi.Strip(v.Render())
		for _, want := range []string{"› ● reviewer", "s stop", "r approval", "Esc close"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("width %d hid selection/action %q:\n%s", width, want, plain)
			}
		}
		if strings.Count(plain, "\n")+1 > 12 {
			t.Fatal("viewer exceeded terminal height")
		}
		v.HandleKeypress(taskKey("s"))
		plain = ansi.Strip(v.Render())
		for _, want := range []string{"Stop task 7?", "[Back]", "Stop task", "Esc back"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("width %d hid confirmation %q:\n%s", width, want, plain)
			}
		}
	}
}

func TestTaskViewerSettledDetailsRetainResultsNotProgress(t *testing.T) {
	for _, status := range []task.TaskStatus{task.StatusCompleted, task.StatusStopped, task.StatusKilled, task.StatusFailed} {
		var v TaskViewer
		v.Show([]TaskViewItem{{ID: "review", AgentName: "agent", Subject: "Security review", Status: status, Progress: "Thinking...", Output: "Useful partial finding"}}, 80, 24)
		v.HandleKeypress(taskKey("h"))
		v.HandleKeypress(taskKey("enter"))
		view := ansi.Strip(v.Render())
		if strings.Contains(view, "Thinking...") || !strings.Contains(view, "Useful partial finding") || !strings.Contains(view, "Security review") {
			t.Fatalf("%s kept stale progress or hid useful results:\n%s", status, view)
		}
	}
}

func TestTaskViewerStopIsNotAnError(t *testing.T) {
	var v TaskViewer
	item := TaskViewItem{ID: "review", AgentName: "agent", Subject: "Security review", Status: "stopped", Error: "stopped before completion", Output: "Error: context canceled"}
	v.Show([]TaskViewItem{item}, 80, 24)
	v.HandleKeypress(taskKey("h"))
	v.HandleKeypress(taskKey("enter"))
	view := ansi.Strip(v.Render())
	if !strings.Contains(view, "Status: stopped") || strings.Contains(view, "Error:") || strings.Contains(view, "context canceled") {
		t.Fatalf("ordinary stop looked like a failure:\n%s", view)
	}
	item.Error, item.Output = "provider unavailable", "Useful partial finding\nError: context canceled"
	v.Refresh([]TaskViewItem{item})
	view = ansi.Strip(v.Render())
	for _, want := range []string{"Reason: provider unavailable", "Useful partial finding"} {
		if !strings.Contains(view, want) {
			t.Fatalf("stopped task hid substantive detail %q:\n%s", want, view)
		}
	}
}

func TestTaskViewerCurrentPrefersLiveSnapshot(t *testing.T) {
	v := TaskViewer{selected: "same", items: []TaskViewItem{{ID: "same", Output: "live"}},
		archived: map[string]TaskViewItem{"same": {ID: "same", Output: "archived"}}}
	if item, ok := v.current(); !ok || item.Output != "live" {
		t.Fatal("current task did not prefer the live snapshot")
	}
	v.items = nil
	if item, ok := v.current(); !ok || item.Output != "archived" {
		t.Fatal("missing task lost its archived snapshot")
	}
}

func TestTaskViewerUnknownStatus(t *testing.T) {
	for _, status := range []task.TaskStatus{"", "future-status"} {
		want := string(status)
		if want == "" {
			want = "unknown"
		}
		if got := taskViewStatus(TaskViewItem{Status: status}); got != want {
			t.Fatalf("status %q displayed as %q, want %q", status, got, want)
		}
	}
}

func TestTaskViewerReviewScopes(t *testing.T) {
	for _, tc := range []struct {
		status                task.TaskStatus
		waiting, queued, want bool
	}{{"running", true, false, true}, {"running", true, true, true}, {"running", false, false, false}, {"stopped", true, false, false}} {
		var v TaskViewer
		v.Show([]TaskViewItem{{ID: "exact", Status: tc.status, AwaitingApproval: tc.waiting, ApprovalQueued: tc.queued}}, 80, 20)
		cmd := v.HandleKeypress(taskKey("r"))
		if (cmd != nil) != tc.want {
			t.Fatal("incorrect review scope")
		}
		if cmd != nil && cmd().(TaskReviewApprovalMsg).TaskID != "exact" {
			t.Fatal("wrong review identity")
		}
	}
}
