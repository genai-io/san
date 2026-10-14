package session

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
)

func TestHistoryIncludesCompactedResultsAndHydratesFullOutput(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := &History{
		Metadata: Metadata{ID: "history-test"},
		Messages: []core.Message{
			{ID: "prompt", Role: ai.RoleUser, Content: ai.TextContent("run a command")},
			{ID: "call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "tc", Name: "Bash", Input: `{"command":"test"}`})}},
			{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "tc", ToolName: "Bash", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/history-test/tc]")})}},
			{ID: "summary", Role: ai.RoleUser, Content: ai.TextContent("compacted summary")},
		},
	}
	if err := store.importHistory(snap); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult(snap.Metadata.ID, "tc", "full output\nLAST_LINE"); err != nil {
		t.Fatal(err)
	}
	if err := store.transcriptStore.Append(context.Background(), transcript.Record{SessionID: snap.Metadata.ID, Time: time.Now(), Type: transcript.SessionCompacted, Compaction: &transcript.Compaction{MessageID: "summary"}}); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Read(snap.Metadata.ID)
	if err != nil || len(resumed.Messages) != 1 || resumed.Messages[0].ID != "summary" {
		t.Fatalf("resume should still stop at compaction: %+v, %v", resumed, err)
	}
	history, err := store.ReadChatHistory(snap.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || history[2].ToolResult == nil || history[2].ToolResult.ToolName != "Bash" || history[2].ToolResult.Content.Text() != "full output\nLAST_LINE" {
		t.Fatalf("history lost the compacted full result: %+v", history)
	}
	// Reading history must not change the persisted resume boundary.
	resumed, err = store.Read(snap.Metadata.ID)
	if err != nil || len(resumed.Messages) != 1 {
		t.Fatalf("reading history changed resume: %+v, %v", resumed, err)
	}
}

func TestDisplayHydratesOnlyValidToolReferences(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult("s", "ok", "full"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, text, want string }{
		{"valid", "preview\n\n[Full output persisted to blobs/tool-result/s/ok]", "full"},
		{"missing", "preview\n\n[Full output persisted to blobs/tool-result/s/missing]", ""},
		{"other-session", "preview\n\n[Full output persisted to blobs/tool-result/other/ok]", ""},
		{"traversal", "preview\n\n[Full output persisted to blobs/tool-result/s/../s/ok]", ""},
		{"unfinished", "preview\n\n[Full output persisted to blobs/tool-result/s/ok", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &History{Metadata: Metadata{ID: "s"}, Messages: []ai.Message{{ID: "r", Role: ai.RoleUser, Content: ai.Content{
				ai.ToolResultBlock(ai.ToolResult{ToolCallID: "one", Content: ai.TextContent(tc.text)}),
				ai.ToolResultBlock(ai.ToolResult{ToolCallID: "two", Content: ai.TextContent(tc.text)}),
			}}}}
			original := h.Messages[0].Content.Clone()
			rows := store.ChatRows(h)
			want := tc.want
			if want == "" {
				want = tc.text
			}
			if len(rows) != 2 || rows[0].ToolResult.Text() != want || rows[1].ToolResult.Text() != want {
				t.Fatalf("incorrect rows: %+v", rows)
			}
			if !reflect.DeepEqual(h.Messages[0].Content, original) {
				t.Fatal("display mutated native content")
			}
		})
	}
}
