package app

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestAgentIdentityRootProjection(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.conv.Messages = []core.ChatMessage{
		{ID: "launch", Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "spawn", Name: "Agent", Input: `{"name":"reviewer"}`}}},
		{ID: "done", Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "spawn", ToolName: "Agent"}, ToolDetails: toolresult.AgentDetails{Identity: toolresult.AgentIdentity{ID: "route", Name: "reviewer", CallID: "spawn", SessionID: "child"}}},
	}
	first := m.messageRenderParams().AgentIdentities
	if first["route"].ID != "route" || first["child"] != first["spawn"] {
		t.Fatalf("missing root aliases: %#v", first)
	}
	_ = m.historyEntries(m.conv.Messages)
	second := m.messageRenderParams().AgentIdentities
	if second["route"] != first["route"] {
		t.Fatal("history changed live projection")
	}
}
