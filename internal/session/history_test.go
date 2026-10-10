package session

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session/transcript"
)

func TestHistoryIncludesCompactedResultsAndHydratesFullOutput(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{
		Metadata: SessionMetadata{ID: "history-test"},
		Messages: []core.ChatMessage{
			{ID: "prompt", Role: core.ChatUser, Content: "run a command"},
			{ID: "call", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "tc", Name: "Bash", Input: `{"command":"test"}`}}},
			{ID: "result", Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "tc", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/history-test/tc]")}},
			{ID: "summary", Role: core.ChatUser, Content: "compacted summary"},
		},
	}
	if err := store.Save(snap); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult(snap.Metadata.ID, "tc", "full output\nLAST_LINE"); err != nil {
		t.Fatal(err)
	}
	if err := store.transcriptStore.Compact(context.Background(), transcript.CompactCommand{SessionID: snap.Metadata.ID, SummaryMessageID: "summary", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Load(snap.Metadata.ID)
	if err != nil || len(resumed.Messages) != 1 || resumed.Messages[0].ID != "summary" {
		t.Fatalf("resume should still stop at compaction: %+v, %v", resumed, err)
	}
	history, err := store.LoadHistory(snap.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || history[2].ToolResult == nil || history[2].ToolResult.ToolName != "Bash" || history[2].ToolResult.Content.Text() != "full output\nLAST_LINE" {
		t.Fatalf("history lost the compacted full result: %+v", history)
	}
	// Reading history must not change the persisted resume boundary.
	resumed, err = store.Load(snap.Metadata.ID)
	if err != nil || len(resumed.Messages) != 1 {
		t.Fatalf("reading history changed resume: %+v, %v", resumed, err)
	}
}
