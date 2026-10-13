package transcript

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestProjectStartAndAppendMessages(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 0, 0, 0, time.UTC)
	transcript, err := Project([]Record{
		{
			ID:        "tx-1:start",
			SessionID: "tx-1",
			Time:      now,
			Type:      SessionStarted,
			Cwd:       "/tmp/project",
			Start:     &SessionStart{Provider: "openai", Model: "gpt-test"},
		},
		{
			ID:        "rec-1",
			SessionID: "tx-1",
			Time:      now.Add(time.Second),
			Type:      MessageAppended,
			Message:   &ai.Message{ID: "msg-1", Role: "user", Content: ai.Content{ai.TextBlock("hello")}},
		},
		{
			ID:              "rec-2",
			SessionID:       "tx-1",
			Time:            now.Add(2 * time.Second),
			Type:            MessageAppended,
			ParentMessageID: "msg-1",
			Message:         &ai.Message{ID: "msg-2", Role: "assistant", Content: ai.Content{ai.TextBlock("world")}},
		},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if transcript.ID != "tx-1" || transcript.Provider != "openai" || transcript.Model != "gpt-test" {
		t.Fatalf("unexpected transcript metadata: %+v", transcript)
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(transcript.Messages))
	}
	if transcript.Messages[1].ParentMessageID != "msg-1" {
		t.Fatalf("unexpected parent chain: %+v", transcript.Messages)
	}
}

func TestProjectStatePatchLastWins(t *testing.T) {
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchTitle("A"), PatchMode("normal")}}},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchTitle("B"), PatchMode("plan")}}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if transcript.State.Title != "B" || transcript.State.Mode != "plan" {
		t.Fatalf("unexpected state: %+v", transcript.State)
	}
}

func TestProjectAutoPilotRoundTrip(t *testing.T) {
	blob := `{"mission":"ship it","steers":{"turnEnd":true}}`
	ops := StateOpsDiff(State{}, State{AutoPilot: blob})
	tr, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: ops}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if tr.State.AutoPilot != blob {
		t.Fatalf("autoReview not restored: %q", tr.State.AutoPilot)
	}
	if MetadataFromTranscript(tr).AutoPilot != blob {
		t.Fatalf("autoReview missing from metadata view")
	}
}

func TestProjectTasksAndWorktreePatches(t *testing.T) {
	taskTime := time.Date(2026, 4, 6, 14, 10, 0, 0, time.UTC)
	task := TodoItemSnapshot{
		ID:              "1",
		Subject:         "Refactor",
		Status:          "in_progress",
		Metadata:        map[string]any{"task_id": "bg-1"},
		CreatedAt:       taskTime,
		UpdatedAt:       taskTime,
		StatusChangedAt: taskTime,
	}
	wt := &WorktreeState{OriginalCwd: "/repo", WorktreePath: "/repo/.wt/1", WorktreeName: "fix-1"}
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchTasks([]TodoItemSnapshot{task}), PatchWorktree(wt)}}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.State.Tasks) != 1 || transcript.State.Tasks[0].Subject != "Refactor" {
		t.Fatalf("unexpected tasks: %+v", transcript.State.Tasks)
	}
	if transcript.State.Tasks[0].Metadata["task_id"] != "bg-1" {
		t.Fatalf("task metadata was not projected: %+v", transcript.State.Tasks[0].Metadata)
	}
	if transcript.State.Worktree == nil || transcript.State.Worktree.WorktreeName != "fix-1" {
		t.Fatalf("unexpected worktree: %+v", transcript.State.Worktree)
	}
}

func TestProjectWorktreeNullClears(t *testing.T) {
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchWorktree(&WorktreeState{WorktreeName: "a"})}}},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchWorktree(nil)}}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if transcript.State.Worktree != nil {
		t.Fatalf("expected cleared worktree, got %+v", transcript.State.Worktree)
	}
}

func TestProjectCompactBoundaryTruncatesActiveChain(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 20, 0, 0, time.UTC)
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: now, Type: SessionStarted},
		{SessionID: "tx-1", Time: now.Add(time.Second), Type: MessageAppended, Message: &ai.Message{ID: "m1", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(2 * time.Second), Type: MessageAppended, ParentMessageID: "m1", Message: &ai.Message{ID: "m2", Role: "assistant"}},
		{SessionID: "tx-1", Time: now.Add(3 * time.Second), Type: MessageAppended, ParentMessageID: "m2", Message: &ai.Message{ID: "m3", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(4 * time.Second), Type: SessionCompacted, Compaction: &Compaction{MessageID: "m2"}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Messages[0].ID != "m2" || transcript.Messages[1].ID != "m3" {
		t.Fatalf("unexpected active chain: %+v", transcript.Messages)
	}
}

func TestProjectUnknownPatchPathIsRejected(t *testing.T) {
	_, err := Project([]Record{{SessionID: "tx-1", Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{{Path: "bad.path", Value: json.RawMessage(`"x"`)}}}}})
	if err == nil {
		t.Fatal("unknown state path accepted")
	}
}

func TestProjectLatestLeafWins(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 30, 0, 0, time.UTC)
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: now, Type: SessionStarted},
		{SessionID: "tx-1", Time: now.Add(time.Second), Type: MessageAppended, Message: &ai.Message{ID: "m1", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(2 * time.Second), Type: MessageAppended, ParentMessageID: "m1", Message: &ai.Message{ID: "m2", Role: "assistant"}},
		{SessionID: "tx-1", Time: now.Add(3 * time.Second), Type: MessageAppended, ParentMessageID: "m1", Message: &ai.Message{ID: "m3", Role: "assistant"}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Messages[1].ID != "m3" {
		t.Fatalf("expected latest leaf m3, got %+v", transcript.Messages)
	}
}

func TestHistoryFollowsSelectedBranchAcrossCompactions(t *testing.T) {
	records := []Record{
		{Type: MessageAppended, Message: &ai.Message{ID: "start", Role: "user"}},
		{Type: MessageAppended, ParentMessageID: "start", Message: &ai.Message{ID: "abandoned", Role: "assistant"}},
		{Type: MessageAppended, ParentMessageID: "start", Message: &ai.Message{ID: "selected", Role: "assistant"}},
		{Type: SessionCompacted, Compaction: &Compaction{MessageID: "selected"}},
		{Type: MessageAppended, ParentMessageID: "selected", Message: &ai.Message{ID: "summary2", Role: "user"}},
		{Type: SessionCompacted, Compaction: &Compaction{MessageID: "summary2"}},
		{Type: MessageAppended, ParentMessageID: "summary2", Message: &ai.Message{ID: "last", Role: "assistant"}},
	}
	history, err := project(records, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"start", "selected", "summary2", "last"}
	if len(history.Messages) != len(want) {
		t.Fatalf("history has %d messages, want %d", len(history.Messages), len(want))
	}
	for i, node := range history.Messages {
		if node.ID != want[i] {
			t.Fatalf("history followed the wrong branch: %+v", history.Messages)
		}
	}
	resumed, err := Project(records)
	if err != nil || len(resumed.Messages) != 2 || resumed.Messages[0].ID != "summary2" {
		t.Fatalf("resume boundary changed: %+v, %v", resumed, err)
	}
}
