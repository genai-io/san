package session

import (
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/todo"
)

func TestTrackerItemViewsRoundTripPreservesMetadata(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 30, 0, 0, time.UTC)
	items := []todo.Item{{
		ID:              "1",
		Subject:         "Refactor",
		Description:     "Move projection helpers",
		ActiveForm:      "Refactoring",
		Status:          todo.StatusInProgress,
		Owner:           "main",
		Metadata:        map[string]any{"task_id": "bg-1"},
		Blocks:          []string{"2"},
		BlockedBy:       []string{"3"},
		CreatedAt:       now,
		UpdatedAt:       now,
		StatusChangedAt: now,
	}}

	views := trackerItemViewsFromItems(items)
	roundTrip := trackerItemsFromViews(views)
	if !reflect.DeepEqual(roundTrip, items) {
		t.Fatalf("task roundtrip mismatch:\n got: %+v\nwant: %+v", roundTrip, items)
	}

	views[0].Metadata["task_id"] = "changed"
	if items[0].Metadata["task_id"] != "bg-1" {
		t.Fatal("conversion aliased task metadata")
	}
}
