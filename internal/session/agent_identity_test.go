package session

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestAgentIdentitySurvivesResumeAndForkWithoutModelLeak(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := mustSession(t, store.transcriptStore, CreateOptions{ID: "identity-source", AgentID: "main"})
	identity := toolresult.AgentIdentity{ID: "sender-hook", Name: "worker", CallID: "original-launch", SessionID: "child-session"}
	w.RecordEvent(sdkagent.ToolEnd{ID: "call", Name: "Agent", Result: sdkagent.Result{Details: toolresult.AgentDetails{Identity: identity}}})
	result := core.ToolResultMessage(core.ToolResult{ToolCallID: "call", ToolName: "Agent", Content: ai.TextContent("finding only")})
	result.ID = "result"
	w.RecordEvent(sdkagent.MessageAdded{Message: result})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check := func(h *History) {
		t.Helper()
		rows := h.ChatRows()
		if len(rows) != 1 {
			t.Fatalf("rows: %d", len(rows))
		}
		details, ok := rows[0].ToolDetails.(toolresult.AgentDetails)
		if !ok || details.Identity != identity {
			t.Fatalf("identity changed: %#v", rows[0].ToolDetails)
		}
		if rows[0].ToolResult == nil || rows[0].ToolResult.Text() != "finding only" {
			t.Fatal("identity leaked into model result")
		}
	}
	h, err := store.Read("identity-source")
	if err != nil {
		t.Fatal(err)
	}
	check(h)
	child, h, err := store.Fork("identity-source")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	check(h)
}
