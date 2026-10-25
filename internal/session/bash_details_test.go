package session

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestBashTailSurvivesResumeAndFork(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "source", AgentID: "main"})
	details := toolresult.BashDetails{LineCount: 128, Tail: "last one\nlast two\n"}
	w.RecordEvent(sdkagent.ToolEnd{ID: "call", Name: "Bash", Result: sdkagent.Result{Details: details}})
	result := core.ToolResultMessage(core.ToolResult{ToolCallID: "call", ToolName: "Bash", Content: ai.TextContent("model-facing output")})
	result.ID = "result"
	w.RecordEvent(sdkagent.MessageAdded{Message: result})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check := func(history *History) {
		t.Helper()
		rows := history.ChatRows()
		if len(rows) != 1 {
			t.Fatalf("got %d rows", len(rows))
		}
		if got, ok := rows[0].ToolDetails.(toolresult.BashDetails); !ok || got != details {
			t.Fatalf("details changed: %#v", rows[0].ToolDetails)
		}
		if rows[0].ToolResult == nil || rows[0].ToolResult.Text() != "model-facing output" {
			t.Fatal("native result changed")
		}
	}
	history, err := store.Read("source")
	if err != nil {
		t.Fatal(err)
	}
	check(history)
	forkSession, fork, err := store.Fork("source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forkSession.Close() })
	check(fork)
}
