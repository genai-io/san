package transcript

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRecordJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	rec := Record{
		ID:        "rec-1",
		SessionID: "tx-1",
		Time:      now,
		Type:      MessageAppended,
		ParentID:  "msg-0",
		Cwd:       "/tmp/project",
		GitBranch: "main",
		Message: &MessageRecord{
			MessageID: "msg-1",
			Role:      "assistant",
			Content: []ContentBlock{
				{Type: "text", Text: "hello"},
				{Type: "tool_use", ID: "tool-1", Name: "read", Input: json.RawMessage(`{"path":"a.txt"}`)},
			},
		},
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}

	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(): %v", err)
	}

	if got.ID != rec.ID || got.SessionID != rec.SessionID || got.Type != rec.Type {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, rec)
	}
	if got.Message == nil || len(got.Message.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %+v", got.Message)
	}
	if string(got.Message.Content[1].Input) != `{"path":"a.txt"}` {
		t.Fatalf("unexpected tool input: %s", string(got.Message.Content[1].Input))
	}
}

func TestPatchPathConstantsAreStable(t *testing.T) {
	if PatchPathTitle != "title" {
		t.Fatalf("PatchPathTitle = %q", PatchPathTitle)
	}
	if PatchPathWorktree != "worktree" {
		t.Fatalf("PatchPathWorktree = %q", PatchPathWorktree)
	}
}
