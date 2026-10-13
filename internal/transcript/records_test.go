package transcript

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestRecordJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	rec := Record{
		ID:              "rec-1",
		SessionID:       "tx-1",
		Time:            now,
		Type:            MessageAppended,
		ParentMessageID: "msg-0",
		Cwd:             "/tmp/project",
		GitBranch:       "main",
		Message:         &ai.Message{ID: "msg-1", Role: "assistant", Content: ai.Content{ai.TextBlock("hello"), ai.ToolCallBlock(ai.ToolCall{ID: "tool-1", Name: "read", Input: string(json.RawMessage(`{"path":"a.txt"}`))})}},
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
	if string(got.Message.Content[1].ToolCall.Input) != `{"path":"a.txt"}` {
		t.Fatalf("unexpected tool input: %s", string(got.Message.Content[1].ToolCall.Input))
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

func TestToolSchemaUsesSDKShape(t *testing.T) {
	rec := Record{
		ID:        "rec-1",
		SessionID: "tx-1",
		Type:      ToolAdded,
		Tool: &ToolRecord{Schema: &ai.Schema{
			Name:        "Read",
			Description: "",
			Definition:  json.RawMessage(`{"type":"object"}`),
		}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid json: %s", data)
	}
	if got := string(data); !containsAll(got, `"definition":{"type":"object"}`) {
		t.Fatalf("tool schema JSON did not preserve model shape: %s", got)
	}

}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}
