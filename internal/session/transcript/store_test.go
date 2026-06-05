package transcript

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/genai-io/san/v2/internal/task/tracker"
)

func TestPatchHelpersEncodeExpectedPayloads(t *testing.T) {
	taskTime := time.Date(2026, 4, 6, 13, 0, 0, 0, time.UTC)
	task := tracker.Task{
		ID:              "1",
		Subject:         "Refactor",
		Status:          tracker.StatusInProgress,
		CreatedAt:       taskTime,
		UpdatedAt:       taskTime,
		StatusChangedAt: taskTime,
	}

	cases := []struct {
		name string
		got  PatchOp
		want string
	}{
		{name: "title", got: PatchTitle("New Title"), want: `"New Title"`},
		{name: "lastPrompt", got: PatchLastPrompt("continue"), want: `"continue"`},
		{name: "mode", got: PatchMode("plan"), want: `"plan"`},
		{name: "tag", got: PatchTag("urgent"), want: `"urgent"`},
	}

	for _, tc := range cases {
		if got := string(tc.got.Value); got != tc.want {
			t.Fatalf("%s payload = %s, want %s", tc.name, got, tc.want)
		}
	}

	taskPatch := PatchTasks([]tracker.Task{task})
	var decodedTasks []tracker.Task
	if err := json.Unmarshal(taskPatch.Value, &decodedTasks); err != nil {
		t.Fatalf("Unmarshal(task patch): %v", err)
	}
	if len(decodedTasks) != 1 || decodedTasks[0].Subject != "Refactor" {
		t.Fatalf("unexpected task patch payload: %+v", decodedTasks)
	}

	wtPatch := PatchWorktree(&WorktreeState{
		OriginalCwd:  "/repo",
		WorktreePath: "/repo/.wt/1",
		WorktreeName: "fix-1",
	})
	var wt WorktreeState
	if err := json.Unmarshal(wtPatch.Value, &wt); err != nil {
		t.Fatalf("Unmarshal(worktree patch): %v", err)
	}
	if wt.WorktreeName != "fix-1" {
		t.Fatalf("unexpected worktree payload: %+v", wt)
	}
}

func Test_PatchWorktreeAllowsNil(t *testing.T) {
	patch := PatchWorktree(nil)
	if string(patch.Value) != "null" {
		t.Fatalf("expected null payload, got %s", string(patch.Value))
	}
}
