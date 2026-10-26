package input

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
)

func TestUIReviewTaskRefreshPreservesDeliberatePageScroll(t *testing.T) {
	items := make([]TaskViewItem, 25)
	for i := range items {
		items[i] = TaskViewItem{ID: fmt.Sprintf("worker%d", i), Subject: fmt.Sprintf("worker%d", i), Status: task.StatusRunning, Output: strings.Repeat("output\n", 40)}
	}
	var v TaskViewer
	v.Show(items, 80, 12)
	v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	offset := v.body.YOffset()
	if offset == 0 || strings.Contains(ansi.Strip(v.body.View()), "› ● worker0") {
		t.Fatal("page scrolling did not hide unchanged selection")
	}
	v.Refresh(items)
	if v.selected != "worker0" || v.body.YOffset() != offset {
		t.Fatalf("refresh reset page scroll: selection %s, offset %d, want %d", v.selected, v.body.YOffset(), offset)
	}
	v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	if v.selected != "worker1" || !strings.Contains(ansi.Strip(v.body.View()), "› ● worker1") {
		t.Fatal("arrow navigation did not restore selection visibility")
	}
	v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	offset = v.body.YOffset()
	if offset == 0 {
		t.Fatal("detail did not scroll")
	}
	v.Refresh(items)
	if !v.detail || v.body.YOffset() != offset {
		t.Fatal("refresh reset detail scrolling")
	}
}

func TestUIReviewTaskAsyncCompletionSelectionVisible(t *testing.T) {
	for _, collapse := range []bool{false, true} {
		t.Run(fmt.Sprint(collapse), func(t *testing.T) {
			items := make([]TaskViewItem, 25)
			for i := range items {
				items[i] = TaskViewItem{ID: fmt.Sprintf("worker%d", i), Subject: fmt.Sprintf("worker%d", i), Status: task.StatusRunning, AwaitingApproval: true}
			}
			var v TaskViewer
			v.Show(items, 80, 12)
			for range 24 {
				v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			if v.selected != "worker24" || v.body.YOffset() == 0 {
				t.Fatalf("did not navigate to last: %s/%d", v.selected, v.body.YOffset())
			}
			if collapse {
				v.HandleKeypress(tea.KeyPressMsg{Code: 'h', Text: "h"})
			}
			items[24].Status = task.StatusCompleted
			v.Refresh(items)
			if collapse {
				if v.selected != "worker24" {
					t.Fatal("expanded history lost identity")
				}
				v.HandleKeypress(tea.KeyPressMsg{Code: 'h', Text: "h"})
			}
			if v.selected != "worker0" || !strings.Contains(ansi.Strip(v.body.View()), "› ● worker0") {
				t.Fatalf("selection invisible: %s offset %d: %s", v.selected, v.body.YOffset(), v.body.View())
			}
			v.Refresh(items)
			if !strings.Contains(ansi.Strip(v.body.View()), "› ● worker0") {
				t.Fatal("refresh hid selection")
			}
			v.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !v.detail || !strings.Contains(ansi.Strip(v.body.View()), "worker0") {
				t.Fatal("Enter did not open visible worker")
			}
		})
	}
}
