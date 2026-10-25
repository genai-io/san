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
	before := ansi.Strip(RenderTrackerList(params))
	if !strings.Contains(before, "[failed]") || !strings.Contains(before, info.Error) || strings.Contains(before, "History") {
		t.Fatalf("new failure lost its evidence before announcement:\n%s", before)
	}
	shown = true
	after := ansi.Strip(RenderTrackerList(params))
	if after != "  History · 1 · /tasks\n" {
		t.Fatalf("announced failure did not become one quiet row: %q", after)
	}
	if item := store.List()[0]; todo.BackgroundStatusDetail(item) != "failed" || todo.BackgroundError(item) != info.Error {
		t.Fatal("history folding mutated the stored failure")
	}
	params.CompletionShown = nil
	if RenderTrackerList(params) != RenderTrackerList(TrackerListParams{Items: params.Items, Width: 100, CompletionShown: func(string) bool { return true }}) {
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
		plain := ansi.Strip(RenderTrackerList(TrackerListParams{Items: store.List(), Width: width, Executing: func(*todo.Item) bool { return true }}))
		for _, want := range []string{"Current review", "Pending plan", "History · 30 · /tasks"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("width %d lost %q:\n%s", width, want, plain)
			}
		}
		if strings.Index(plain, "History") < strings.Index(plain, "Pending plan") || strings.Contains(plain, "Past work") || strings.Contains(plain, "failed/stopped") {
			t.Fatalf("history displaced or cluttered current work:\n%s", plain)
		}
		for _, row := range strings.Split(plain, "\n") {
			if ansi.StringWidth(row) > width {
				t.Fatalf("summary overflowed width %d: %q", width, row)
			}
		}
	}
}

func TestTrackerHistoryDoesNotCallStopsFailures(t *testing.T) {
	store := todo.NewStore()
	for _, status := range []task.TaskStatus{task.StatusStopped, task.StatusKilled, "interrupted"} {
		info := task.TaskInfo{ID: string(status), Description: "Old review", Status: task.StatusRunning}
		todo.TrackWorker(store, info)
		info.Status = status
		todo.CompleteWorker(store, info)
	}
	if plain := ansi.Strip(RenderTrackerList(TrackerListParams{Items: store.List(), Width: 32})); plain != "  History · 3 · /tasks\n" {
		t.Fatalf("stopped/interrupted history was noisy or labelled failure: %q", plain)
	}
	success := todo.NewStore()
	info := task.TaskInfo{ID: "done", Description: "Succeeded", Status: task.StatusRunning}
	todo.TrackWorker(success, info)
	info.Status = task.StatusCompleted
	todo.CompleteWorker(success, info)
	if got := RenderTrackerList(TrackerListParams{Items: success.List(), Width: 32}); got != "" {
		t.Fatalf("successful history added permanent footer clutter: %q", got)
	}
}
