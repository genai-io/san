package session

import (
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
)

// A turn answering two parallel calls is one agent message with two
// tool_result blocks. The writer must persist both, and a load must hand
// them back as two rows — otherwise the resumed chain carries one orphaned
// tool_use, which the SDK's repair pass then drops along with its result.
func TestParallelToolResultsSurviveRecordAndLoad(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	const sessionID = "sess-parallel"
	rec := NewWriter(WriterOptions{
		Store: store.transcriptStore, SessionID: sessionID, AgentID: "main",
		Provider: "p", Model: "m", Cwd: "/tmp",
	})

	defer rec.Close()

	calls := core.Message{ID: "a1", Role: ai.RoleAssistant, Content: ai.Content{
		ai.ToolCallBlock(core.ToolCall{ID: "c1", Name: "Read", Input: `{}`}),
		ai.ToolCallBlock(core.ToolCall{ID: "c2", Name: "Bash", Input: `{}`}),
	}}
	results := ai.ToolResultsMessage(
		core.ToolResult{ToolCallID: "c1", ToolName: "Read", Content: ai.TextContent("one")},
		core.ToolResult{ToolCallID: "c2", ToolName: "Bash", Content: ai.TextContent("two")},
	)
	results.ID = "r1"
	for _, msg := range []core.Message{
		{ID: "u1", Role: ai.RoleUser, Content: ai.TextContent("go")}, calls, results,
	} {
		rec.OnAgentEvent(sdkagent.MessageAdded{Message: msg})
	}

	loaded, err := store.Load(sessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var got []string
	for _, row := range ChatRows(loaded.Messages) {
		if row.ToolResult != nil {
			got = append(got, row.ToolResult.ToolCallID+"="+row.ToolResult.Content.Text()+"/"+row.ToolResult.ToolName)
		}
	}
	want := []string{"c1=one/Read", "c2=two/Bash"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tool result rows = %v, want %v", got, want)
	}

	// Retire the live writer before importing a cold native snapshot.
	rec.Close()
	if err := store.Save(&Snapshot{Metadata: Metadata{ID: sessionID}, Messages: loaded.Messages}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := store.Load(sessionID)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if len(again.Messages) != len(loaded.Messages) {
		t.Fatalf("re-saved rows = %d, want %d", len(again.Messages), len(loaded.Messages))
	}
}
