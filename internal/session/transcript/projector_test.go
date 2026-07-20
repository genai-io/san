package transcript

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/genai-io/san/internal/todo"
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
			Session:   &SessionRecord{Provider: "openai", Model: "gpt-test"},
		},
		{
			ID:        "rec-1",
			SessionID: "tx-1",
			Time:      now.Add(time.Second),
			Type:      MessageAppended,
			Message:   &MessageRecord{MessageID: "msg-1", Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
		},
		{
			ID:        "rec-2",
			SessionID: "tx-1",
			Time:      now.Add(2 * time.Second),
			Type:      MessageAppended,
			ParentID:  "msg-1",
			Message:   &MessageRecord{MessageID: "msg-2", Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "world"}}},
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
	if transcript.Messages[1].ParentID != "msg-1" {
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
	task := todo.Item{
		ID:              "1",
		Subject:         "Refactor",
		Status:          todo.StatusInProgress,
		CreatedAt:       taskTime,
		UpdatedAt:       taskTime,
		StatusChangedAt: taskTime,
	}
	wt := &WorktreeState{OriginalCwd: "/repo", WorktreePath: "/repo/.wt/1", WorktreeName: "fix-1"}
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchTasks([]todo.Item{task}), PatchWorktree(wt)}}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.State.Tasks) != 1 || transcript.State.Tasks[0].Subject != "Refactor" {
		t.Fatalf("unexpected tasks: %+v", transcript.State.Tasks)
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
		{SessionID: "tx-1", Time: now.Add(time.Second), Type: MessageAppended, Message: &MessageRecord{MessageID: "m1", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(2 * time.Second), Type: MessageAppended, ParentID: "m1", Message: &MessageRecord{MessageID: "m2", Role: "assistant"}},
		{SessionID: "tx-1", Time: now.Add(3 * time.Second), Type: MessageAppended, ParentID: "m2", Message: &MessageRecord{MessageID: "m3", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(4 * time.Second), Type: SessionCompacted, Session: &SessionRecord{SummaryMessageID: "m2"}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Messages[0].ID != "m2" || transcript.Messages[1].ID != "m3" {
		t.Fatalf("unexpected active chain: %+v", transcript.Messages)
	}
}

// Unknown patch paths are silently ignored so older readers tolerate records
// produced by newer schemas. The rest of the patch list must still apply.
func TestProjectUnknownPatchPathIsIgnored(t *testing.T) {
	tx, err := Project([]Record{
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStarted},
		{SessionID: "tx-1", Time: time.Now(), Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{
			{Path: "bad.path", Value: json.RawMessage(`"x"`)},
			PatchTitle("Still applied"),
		}}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if tx.State.Title != "Still applied" {
		t.Fatalf("Title = %q, want %q (unknown path should not block subsequent ops)", tx.State.Title, "Still applied")
	}
}

func TestProjectLatestLeafWins(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 30, 0, 0, time.UTC)
	transcript, err := Project([]Record{
		{SessionID: "tx-1", Time: now, Type: SessionStarted},
		{SessionID: "tx-1", Time: now.Add(time.Second), Type: MessageAppended, Message: &MessageRecord{MessageID: "m1", Role: "user"}},
		{SessionID: "tx-1", Time: now.Add(2 * time.Second), Type: MessageAppended, ParentID: "m1", Message: &MessageRecord{MessageID: "m2", Role: "assistant"}},
		{SessionID: "tx-1", Time: now.Add(3 * time.Second), Type: MessageAppended, ParentID: "m1", Message: &MessageRecord{MessageID: "m3", Role: "assistant"}},
	})
	if err != nil {
		t.Fatalf("Project(): %v", err)
	}
	if len(transcript.Messages) != 2 || transcript.Messages[1].ID != "m3" {
		t.Fatalf("expected latest leaf m3, got %+v", transcript.Messages)
	}
}
