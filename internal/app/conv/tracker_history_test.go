package conv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func TestTrackerHistoryWaitsForFailureNotice(t *testing.T) {
	store := todo.NewStore()
	info := task.TaskInfo{ID: "review", AgentName: "Reviewer", Description: "Audit permissions", Status: task.StatusRunning}
	todo.TrackWorker(store, info)
	info.Status, info.Error = task.StatusFailed, "permission boundary failed"
	todo.CompleteWorker(store, info)
	shown := false
	params := TrackerListParams{Items: store.List(), Width: 100, CompletionShown: func(string) bool { return shown }}
	before, history := RenderTrackerList(params)
	before = ansi.Strip(before)
	if history != "" || !strings.Contains(before, "[failed]") || !strings.Contains(before, info.Error) {
		t.Fatalf("new failure lost its evidence before announcement: history=%q\n%s", history, before)
	}
	shown = true
	if after, history := RenderTrackerList(params); after != "" || history != "1 failed" {
		t.Fatalf("announced failure should leave the tracker and join history: history=%q rows=%q", history, after)
	}
	if item := store.List()[0]; todo.BackgroundStatusDetail(item) != "failed" || todo.BackgroundError(item) != info.Error {
		t.Fatal("history folding mutated the stored failure")
	}
	params.CompletionShown = nil
	if rows, history := RenderTrackerList(params); rows != "" || history != "1 failed" {
		t.Fatal("nil announcement callback did not mean already announced")
	}
}

func TestTrackerHistoryIsSecondaryToCurrentWork(t *testing.T) {
	for _, width := range []int{32, 40, 100} {
		store := todo.NewStore()
		for i := range 30 {
			info := task.TaskInfo{ID: fmt.Sprint(i), Description: "Past work", Status: task.StatusRunning}
			todo.TrackWorker(store, info)
			info.Status = task.StatusFailed
			todo.CompleteWorker(store, info)
		}
		for _, subject := range []string{"Current review", "Pending plan"} {
			item := store.Create(subject, "", "", nil)
			if subject == "Current review" {
				_ = store.Update(item.ID, todo.WithStatus(todo.StatusInProgress))
			}
		}
		rows, history := RenderTrackerList(TrackerListParams{Items: store.List(), Width: width, Executing: func(*todo.Item) bool { return true }})
		if history != "30 failed" {
			t.Fatalf("width %d: got history %q, want 30 failed", width, history)
		}
		plain := ansi.Strip(rows)
		for _, want := range []string{"Current review", "Pending plan"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("width %d lost %q:\n%s", width, want, plain)
			}
		}
		if strings.Count(plain, "\n") != 3 || strings.Contains(plain, "Past work") || strings.Contains(plain, "failed/stopped") {
			t.Fatalf("history displaced or cluttered current work:\n%s", plain)
		}
		for _, row := range strings.Split(plain, "\n") {
			if ansi.StringWidth(row) > width {
				t.Fatalf("summary overflowed width %d: %q", width, row)
			}
		}
	}
}

func TestTrackerHistoryCountsOnlyAbnormalEndings(t *testing.T) {
	store := todo.NewStore()
	for _, status := range []task.TaskStatus{task.StatusStopped, task.StatusKilled, "interrupted"} {
		info := task.TaskInfo{ID: string(status), Description: "Old review", Status: task.StatusRunning}
		todo.TrackWorker(store, info)
		info.Status = status
		todo.CompleteWorker(store, info)
	}
	if rows, history := RenderTrackerList(TrackerListParams{Items: store.List(), Width: 32}); rows != "" || history != "2 stopped, 1 interrupted" {
		t.Fatalf("stops and interruptions should be distinct from failures: history=%q rows=%q", history, rows)
	}
	success := todo.NewStore()
	info := task.TaskInfo{ID: "done", Description: "Succeeded", Status: task.StatusRunning}
	todo.TrackWorker(success, info)
	info.Status = task.StatusCompleted
	todo.CompleteWorker(success, info)
	if rows, history := RenderTrackerList(TrackerListParams{Items: success.List(), Width: 32}); rows != "" || history != "" {
		t.Fatalf("successful history added permanent footer clutter: history=%q rows=%q", history, rows)
	}
}
