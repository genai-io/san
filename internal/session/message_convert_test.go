package session

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// A user message that contains harness-injected <system-reminder> blocks must
// be persisted as multiple ContentBlocks: user-typed text with empty Source,
// reminder text with Source="reminder". Concatenating the text fields must
// reproduce the input byte-for-byte (round-trip safety for read path).
func Test_userContentToBlocks_splitsByProvenance(t *testing.T) {
	const reminder1 = "<system-reminder>\nskills directory\n</system-reminder>"
	const reminder2 = "<system-reminder>\nuser memory\n</system-reminder>"
	input := "hello\n\n" + reminder1 + "\n\n" + reminder2

	blocks := userContentToBlocks(input, "", nil)

	var sb strings.Builder
	var reminderCount, userCount int
	for _, b := range blocks {
		if b.Type != "text" {
			t.Fatalf("unexpected block type: %q", b.Type)
		}
		sb.WriteString(b.Text)
		switch b.Source {
		case SourceReminder:
			reminderCount++
			if !strings.HasPrefix(b.Text, "<system-reminder>") {
				t.Errorf("reminder block missing wrapper: %q", b.Text)
			}
		case "":
			userCount++
		default:
			t.Fatalf("unexpected Source: %q", b.Source)
		}
	}
	if sb.String() != input {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", sb.String(), input)
	}
	if reminderCount != 2 {
		t.Fatalf("reminder block count = %d, want 2", reminderCount)
	}
	if userCount == 0 {
		t.Fatalf("expected at least one user block, got %d", userCount)
	}
}

// Plain user content with no reminders produces exactly one user-text block.
func Test_userContentToBlocks_plainTextOneBlock(t *testing.T) {
	blocks := userContentToBlocks("just a question", "", nil)
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Source != "" {
		t.Fatalf("expected 1 user-text block, got %+v", blocks)
	}
}

func Test_messagesToEntries_roundtrip(t *testing.T) {
	// Test that messagesToEntries -> EntriesToMessages roundtrips correctly.
	msgs := []core.Message{
		{Role: core.RoleUser, Content: "hello"},
		{Role: core.RoleAssistant, Content: "hi", Thinking: "let me think",
			ToolCalls: []core.ToolCall{{ID: "tc-1", Name: "Read", Input: `{"file_path":"/tmp/test"}`}}},
		{Role: core.RoleUser, ToolResult: &core.ToolResult{
			ToolCallID: "tc-1", ToolName: "Read", Content: "file contents",
		}},
		{Role: core.RoleAssistant, Content: "I see the file."},
	}

	entries := messagesToEntries(msgs)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Verify entry types
	if entries[0].Type != EntryUser {
		t.Errorf("entry[0] type: want user, got %s", entries[0].Type)
	}
	if entries[1].Type != EntryAssistant {
		t.Errorf("entry[1] type: want assistant, got %s", entries[1].Type)
	}
	if entries[2].Type != EntryUser {
		t.Errorf("entry[2] type: want user (tool_result), got %s", entries[2].Type)
	}

	// Round-trip back to messages
	restored := EntriesToMessages(entries)
	if len(restored) != 4 {
		t.Fatalf("expected 4 messages after roundtrip, got %d", len(restored))
	}
	if restored[0].Content != "hello" {
		t.Errorf("msg[0].Content: want 'hello', got %q", restored[0].Content)
	}
	if restored[1].Thinking != "let me think" {
		t.Errorf("msg[1].Thinking: want 'let me think', got %q", restored[1].Thinking)
	}
	if restored[2].ToolResult == nil {
		t.Fatal("msg[2].ToolResult should not be nil")
	}
	if restored[2].ToolResult.ToolCallID != "tc-1" {
		t.Errorf("msg[2].ToolResult.ToolCallID: want 'tc-1', got %q", restored[2].ToolResult.ToolCallID)
	}
	// Tool name should be resolved from the tool_use block
	if restored[2].ToolResult.ToolName != "Read" {
		t.Errorf("msg[2].ToolResult.ToolName: want 'Read', got %q", restored[2].ToolResult.ToolName)
	}
}

func Test_userContentToBlocks_preserveInlineImageOrder(t *testing.T) {
	blocks := userContentToBlocks(
		"这个图片说了什么 请说一下",
		"[Image #1] 这个图片说了什么 请说一下",
		[]core.Image{{MediaType: "image/png", Data: "abc"}},
	)

	if len(blocks) != 2 {
		t.Fatalf("expected image and text blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "image" {
		t.Fatalf("expected first block to be image, got %q", blocks[0].Type)
	}
	if blocks[1].Type != "text" || blocks[1].Text != " 这个图片说了什么 请说一下" {
		t.Fatalf("unexpected second block: %#v", blocks[1])
	}
}

func Test_extractUserContent_restoresDisplayContent(t *testing.T) {
	msgs := EntriesToMessages([]Entry{{
		Type: EntryUser,
		Message: &EntryMessage{
			Role: "user",
			Content: []ContentBlock{
				{Type: "text", Text: "前面 "},
				{Type: "image", ImageSource: &ImageSource{Type: "base64", MediaType: "image/png", Data: "abc"}},
				{Type: "text", Text: " 后面"},
			},
		},
	}})

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "前面  后面" {
		t.Fatalf("unexpected content: %q", msgs[0].Content)
	}
	if msgs[0].DisplayContent != "前面 [Image #1] 后面" {
		t.Fatalf("unexpected display content: %q", msgs[0].DisplayContent)
	}
}
