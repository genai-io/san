package llm

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestSanitizeToolMessagesStripsOrphanedAssistantToolCall(t *testing.T) {
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{
				ID:    "call_orphan",
				Name:  "Read",
				Input: `{}`,
			}},
		},
		{Role: core.RoleUser, Content: "continue"},
	}

	sanitized := SanitizeToolMessages(msgs)
	if len(sanitized) != 1 {
		t.Fatalf("expected orphaned empty assistant tool call to be dropped, got %d messages", len(sanitized))
	}
	if sanitized[0].Role != core.RoleUser || sanitized[0].Content != "continue" {
		t.Fatalf("unexpected sanitized messages: %#v", sanitized)
	}
}

func TestSanitizeToolMessagesKeepsOnlyImmediatelyAnsweredToolCalls(t *testing.T) {
	msgs := []core.Message{
		{
			Role:    core.RoleAssistant,
			Content: "checking",
			ToolCalls: []core.ToolCall{
				{ID: "call_1", Name: "Read", Input: `{}`},
				{ID: "call_2", Name: "Glob", Input: `{}`},
			},
		},
		{
			Role:       core.RoleUser,
			ToolResult: &core.ToolResult{ToolCallID: "call_1", ToolName: "Read", Content: "ok"},
		},
		{Role: core.RoleUser, Content: "next"},
	}

	sanitized := SanitizeToolMessages(msgs)
	if len(sanitized) != 3 {
		t.Fatalf("expected assistant, one tool result, and user message; got %d", len(sanitized))
	}
	if len(sanitized[0].ToolCalls) != 1 || sanitized[0].ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected filtered tool calls: %#v", sanitized[0].ToolCalls)
	}
	if sanitized[1].ToolResult == nil || sanitized[1].ToolResult.ToolCallID != "call_1" {
		t.Fatalf("unexpected filtered tool result: %#v", sanitized[1])
	}
	if sanitized[2].Content != "next" {
		t.Fatalf("expected trailing user message to remain, got %#v", sanitized[2])
	}
}
