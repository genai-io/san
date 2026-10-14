package transcript

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/todo"
)

func TestStatePatchOwnsTasksAndClearsAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Start(ctx, StartCommand{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	tasks := []todo.Item{{ID: "one", Metadata: map[string]any{"nested": map[string]any{"value": "before"}}}}
	patch := &StatePatch{Title: new("title"), Group: new("group"), Tasks: &tasks}
	appendPatch := func(p *StatePatch) {
		t.Helper()
		if err := store.Append(ctx, Record{SessionID: "s", Type: SessionStatePatched, State: p}); err != nil {
			t.Fatal(err)
		}
	}
	appendPatch(patch)
	tasks[0].Metadata["nested"].(map[string]any)["value"] = "after"
	appendPatch(patch)
	loaded, err := store.Load(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.State.Tasks[0].Metadata["nested"].(map[string]any)["value"]; got != "after" {
		t.Fatalf("task edit was skipped: %v", got)
	}
	store, err = NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	appendPatch(&StatePatch{Group: new(""), Tasks: new([]todo.Item)})
	loaded, err = store.Load(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State.Title != "title" || loaded.State.Group != "" || len(loaded.State.Tasks) != 0 {
		t.Fatalf("clear or omitted field changed: %+v", loaded.State)
	}
	records := readRawRecordMaps(t, dir, "s")
	appendPatch(&StatePatch{Title: new("title"), Group: new(""), Tasks: new([]todo.Item{})})
	if after := readRawRecordMaps(t, dir, "s"); len(after) != len(records) {
		t.Fatal("unchanged fields wrote another record")
	}
}
