package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/core"
)

func TestConvertMessagesConvertsToolResultToToolMessage(t *testing.T) {
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{
				ID:    "call_1",
				Name:  "Read",
				Input: `{"file_path":"README.md"}`,
			}},
		},
		{
			Role: core.RoleUser,
			ToolResult: &core.ToolResult{
				ToolCallID: "call_1",
				ToolName:   "Read",
				Content:    "ok",
			},
		},
	}

	converted := ConvertMessages(msgs, "", DefaultAssistantMessage)
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal converted messages: %v", err)
	}
	got := string(raw)

	for _, want := range []string{
		`"role":"assistant"`,
		`"tool_calls"`,
		`"role":"tool"`,
		`"tool_call_id":"call_1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted messages missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"role":"system"`) {
		t.Fatalf("tool result must not be converted to system message:\n%s", got)
	}
}

func TestDropEmptyMessagesRemovesTextOnlyEmptyUserMessages(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "hello"},
		{Role: core.RoleUser, Content: "  \n\t"},
		{Role: core.RoleAssistant, Content: ""},
		{Role: core.RoleUser, Images: []core.Image{{MediaType: "image/png", Data: "abc"}}},
		{Role: core.RoleUser, ToolResult: &core.ToolResult{ToolCallID: "call_1", Content: "ok"}},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call_2", Name: "Read", Input: `{}`}}},
	}

	filtered := DropEmptyMessages(msgs)
	if len(filtered) != 4 {
		t.Fatalf("expected 4 non-empty provider messages, got %d: %#v", len(filtered), filtered)
	}
	if filtered[0].Content != "hello" {
		t.Fatalf("expected first user text to remain, got %#v", filtered[0])
	}
	if len(filtered[1].Images) != 1 {
		t.Fatalf("expected image-only user message to remain, got %#v", filtered[1])
	}
	if filtered[2].ToolResult == nil {
		t.Fatalf("expected tool result message to remain, got %#v", filtered[2])
	}
	if len(filtered[3].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call to remain, got %#v", filtered[3])
	}
}

func TestDropEmptyMessagesDropsThinkingOnlyAssistantMessage(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, Thinking: "pondering..."},
		{Role: core.RoleUser, Content: "are you there?"},
	}

	filtered := DropEmptyMessages(msgs)
	if len(filtered) != 2 {
		t.Fatalf("expected thinking-only assistant message to be dropped, got %d: %#v", len(filtered), filtered)
	}
	if filtered[0].Content != "hi" || filtered[1].Content != "are you there?" {
		t.Fatalf("unexpected surviving messages: %#v", filtered)
	}
}

func TestConvertMessagesOmitsThinkingOnlyAssistantToAvoidDeepSeek400(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "list files"},
		{Role: core.RoleAssistant, Thinking: "thinking about it"},
		{Role: core.RoleUser, Content: "1.18.1?"},
	}

	converted := ConvertMessages(msgs, "", func(msg core.Message) openai.ChatCompletionMessageParamUnion {
		return AssistantMessageWithReasoning(msg, msg.Thinking)
	})
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal converted messages: %v", err)
	}
	got := string(raw)

	if strings.Contains(got, `"reasoning_content"`) {
		t.Fatalf("thinking-only assistant message must be dropped to avoid invalid Chat Completions payload:\n%s", got)
	}
	if strings.Contains(got, `"role":"assistant"`) {
		t.Fatalf("no assistant message should remain in the payload:\n%s", got)
	}
}

func TestConvertMessagesDoesNotSendEmptyTextOnlyUserMessages(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleUser, Content: ""},
		{Role: core.RoleUser, Content: "second"},
	}

	converted := ConvertMessages(msgs, "sys", DefaultAssistantMessage)
	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal converted messages: %v", err)
	}
	got := string(raw)

	if strings.Contains(got, `"content":""`) {
		t.Fatalf("converted messages should not contain empty content:\n%s", got)
	}
	for _, want := range []string{`"content":"first"`, `"content":"second"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted messages missing %s:\n%s", want, got)
		}
	}
}
