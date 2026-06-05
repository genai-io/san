package tasktools

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task/tracker"
)

func useTestTrackerStore(t *testing.T) *tracker.Store {
	t.Helper()
	store := tracker.NewStore()
	if err := store.SetStorageDir(t.TempDir()); err != nil {
		t.Fatalf("SetStorageDir(): %v", err)
	}
	tracker.SetDefault(store)
	t.Cleanup(func() { tracker.SetDefault(store) })
	return store
}

func TestTrackerGetTool_ShowsOwnerAndOpenBlockers(t *testing.T) {
	store := useTestTrackerStore(t)

	blocker := store.Create("Blocker", "finish first", "blocking", nil)
	blocked := store.Create("Blocked", "waits on blocker", "waiting", nil)
	if err := store.Update(blocked.ID, tracker.WithOwner("Explore"), tracker.WithAddBlockedBy([]string{blocker.ID})); err != nil {
		t.Fatalf("Update(blocked): %v", err)
	}

	result := (&TrackerGetTool{}).Execute(context.Background(), map[string]any{
		"taskId": blocked.ID,
	}, "")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "Owner: Explore") {
		t.Fatalf("expected owner in output, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "Blocked by (open): "+blocker.ID) {
		t.Fatalf("expected open blocker in output, got %q", result.Output)
	}
}

func TestTrackerUpdateTool_ParsesJSONBlockedByAndPersistsFields(t *testing.T) {
	store := useTestTrackerStore(t)

	blocker := store.Create("Blocker", "must finish first", "blocking", nil)
	task := store.Create("Implement", "write tests", "writing", nil)

	result := (&TrackerUpdateTool{}).Execute(context.Background(), map[string]any{
		"taskId":       task.ID,
		"status":       tracker.StatusInProgress,
		"owner":        "Plan",
		"description":  "write more tests",
		"addBlockedBy": `["` + blocker.ID + `"]`,
	}, "")

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Metadata.Subtitle != "#"+task.ID+" "+tracker.StatusInProgress {
		t.Fatalf("unexpected subtitle %q", result.Metadata.Subtitle)
	}

	updated, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("expected updated task to exist")
	}
	if updated.Status != tracker.StatusInProgress {
		t.Fatalf("status = %q, want %q", updated.Status, tracker.StatusInProgress)
	}
	if updated.Owner != "Plan" {
		t.Fatalf("owner = %q, want %q", updated.Owner, "Plan")
	}
	if updated.Description != "write more tests" {
		t.Fatalf("description = %q, want %q", updated.Description, "write more tests")
	}
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != blocker.ID {
		t.Fatalf("blockedBy = %#v, want [%q]", updated.BlockedBy, blocker.ID)
	}
}
