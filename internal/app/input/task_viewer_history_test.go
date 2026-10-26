package input

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
)

func TestTaskViewerHistoryCollapsedByDefault(t *testing.T) {
	var v TaskViewer
	items := []TaskViewItem{
		{ID: "old-failure", Subject: "Old failure", Status: task.StatusFailed, CanContinue: true},
		{ID: "active", Subject: "Live worker", Status: task.StatusRunning},
		{ID: "done", Subject: "Finished work", Status: task.StatusCompleted},
		{ID: "stopped", Status: task.StatusStopped},
		{ID: "killed", Status: task.StatusKilled},
		{ID: "interrupted", Status: "interrupted"},
		{ID: "approval", Subject: "Review request", Status: task.StatusRunning, AwaitingApproval: true},
		{ID: "unknown", Subject: "Unknown state", Status: "future-status"},
	}
	v.Show(items, 100, 24)
	plain := ansi.Strip(v.Render())
	for _, want := range []string{"Tasks · 3 active", "Live worker", "awaiting approval", "Unknown state", "History · 5 · 1 failed", "h history"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q:\n%s", want, plain)
		}
	}
	if v.selected != "active" || strings.Contains(plain, "Old failure") || strings.Contains(plain, "Finished work") {
		t.Fatalf("historical rows displaced active work:\n%s", plain)
	}
	for range 20 {
		v.HandleKeypress(taskKey("j"))
	}
	if v.selected != "unknown" || v.HandleKeypress(taskKey("c")) != nil {
		t.Fatal("navigation or continuation reached a hidden historical task")
	}
	if len(v.items) != len(items) || v.archived["old-failure"].Status != task.StatusFailed {
		t.Fatal("collapsing history mutated or removed a failure")
	}
}

func TestTaskViewerHistoryExpansionOrderAndActions(t *testing.T) {
	var v TaskViewer
	items := []TaskViewItem{
		{ID: "older", Subject: "Earlier work", Status: task.StatusCompleted},
		{ID: "latest", Subject: "Latest failure", Status: task.StatusFailed, Error: "provider unavailable", Output: "Useful partial result", OutputFile: "/tmp/task-output.log", CanContinue: true, Attempt: 3, SessionID: "child"},
	}
	v.Show(items, 100, 24)
	if v.selected != "" || v.HandleKeypress(taskKey("c")) != nil {
		t.Fatal("collapsed history retained an actionable selection")
	}
	v.HandleKeypress(taskKey("enter"))
	if v.detail {
		t.Fatal("Enter opened a hidden task")
	}
	v.HandleKeypress(taskKey("h"))
	if v.selected != "latest" || !v.historyExpanded {
		t.Fatal("history did not select the newest launch first")
	}
	v.HandleKeypress(taskKey("enter"))
	plain := ansi.Strip(v.Render())
	for _, want := range []string{"Status: failed", "provider unavailable", "Useful partial result", "Output log: /tmp/task-output.log", "Attempt: 3", "Session: child", "c ask main to continue"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("history lost diagnostic %q:\n%s", want, plain)
		}
	}
	v.HandleKeypress(taskKey("esc"))
	v.HandleKeypress(taskKey("j"))
	if v.selected != "older" {
		t.Fatal("history navigation did not follow newest-first order")
	}
	v.HandleKeypress(taskKey("k"))
	viewID := v.ViewID()
	cmd := v.HandleKeypress(taskKey("c"))
	if cmd == nil {
		t.Fatal("eligible history task cannot request a continuation")
	}
	if msg := cmd().(TaskContinueMsg); msg.TaskID != "latest" || msg.Attempt != 3 || msg.ViewID != viewID {
		t.Fatalf("history changed the action identity: %+v", msg)
	}
	v.Resume(items, 100, 24)
	if !v.historyExpanded || v.selected != "latest" {
		t.Fatal("action return lost the expanded history or stable selection")
	}
	v.HandleKeypress(taskKey("h"))
	if v.selected != "" || v.HandleKeypress(taskKey("c")) != nil {
		t.Fatal("collapsing left a hidden continuation actionable")
	}
	v.Show(items, 100, 24)
	if v.historyExpanded {
		t.Fatal("reopening did not restore the quiet default")
	}
}

func TestTaskViewerHistoryRefreshAndDetailTransitions(t *testing.T) {
	var v TaskViewer
	a := TaskViewItem{ID: "a", Subject: "First", Status: task.StatusRunning, Attempt: 1}
	b := TaskViewItem{ID: "b", Subject: "Second", Status: task.StatusRunning}
	v.Show([]TaskViewItem{a, b}, 80, 24)
	a.Status = task.StatusFailed
	v.Refresh([]TaskViewItem{a, b})
	if v.selected != "b" || v.historyExpanded || strings.Contains(ansi.Strip(v.Render()), "First [failed]") {
		t.Fatal("completion expanded history or displaced the remaining active worker")
	}
	v.HandleKeypress(taskKey("enter"))
	b.Status, b.Error = task.StatusFailed, "real failure"
	v.Refresh([]TaskViewItem{a, b})
	if !v.detail || v.selected != "b" || !strings.Contains(v.Render(), "real failure") {
		t.Fatal("open detail did not retain the newly ended worker")
	}
	v.HandleKeypress(taskKey("esc"))
	if v.selected != "" || v.detail || v.historyExpanded {
		t.Fatal("returning to the list selected hidden history")
	}
	v.HandleKeypress(taskKey("h"))
	v.HandleKeypress(taskKey("enter"))
	b.Status, b.Attempt, b.Error = task.StatusRunning, 2, ""
	v.Refresh([]TaskViewItem{a, b})
	if v.selected != "b" || !v.detail || !strings.Contains(v.Render(), "Attempt: 2") {
		t.Fatal("continued attempt lost its stable detail identity")
	}
	v.HandleKeypress(taskKey("esc"))
	if !v.historyExpanded || v.selected != "b" {
		t.Fatal("refresh discarded the history preference or live selection")
	}
	v.HandleKeypress(tea.KeyPressMsg{Code: 'h', Text: "h", IsRepeat: true})
	if !v.historyExpanded {
		t.Fatal("key repeat flickered the history section")
	}
}

func TestTaskViewerHistoryNarrowAndShort(t *testing.T) {
	for _, width := range []int{32, 40, 100} {
		for _, height := range []int{8, 12, 24} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				var v TaskViewer
				var items []TaskViewItem
				for i := range 20 {
					items = append(items, TaskViewItem{ID: fmt.Sprint(i), Subject: strings.Repeat("Past work ", 5), Status: task.StatusFailed, CanContinue: true})
				}
				v.Show(items, width, height)
				for _, expanded := range []bool{false, true} {
					if expanded {
						v.HandleKeypress(taskKey("h"))
						for range 19 {
							v.HandleKeypress(taskKey("j"))
						}
					}
					plain := ansi.Strip(v.Render())
					if rows := strings.Count(plain, "\n") + 1; rows > height {
						t.Fatalf("expanded=%v has %d rows, max %d:\n%s", expanded, rows, height, plain)
					}
					for _, row := range strings.Split(plain, "\n") {
						if ansi.StringWidth(row) > width {
							t.Fatalf("expanded=%v row exceeds width: %q", expanded, row)
						}
					}
					if !strings.Contains(plain, "h history") || !strings.Contains(plain, "Esc close") {
						t.Fatalf("history/close hint is unreachable:\n%s", plain)
					}
					if expanded && !strings.Contains(plain, "› ●") {
						t.Fatalf("selected historical row scrolled out of view:\n%s", plain)
					}
				}
			})
		}
	}
}
