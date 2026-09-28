package app

import (
	"github.com/genai-io/sdk-go/pkg/ai"

	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
)

// A user turn carries the skills directory and memory files inline, so the
// same bytes are reachable two ways. They must be credited to their own
// category and removed from Messages — counting them twice would inflate the
// conversation and hide where the window actually went.
func TestAddContextUsageAttributesRemindersOnce(t *testing.T) {
	skills := reminder.WrapWithSource("skills directory body", reminder.ProviderSkillsDirectory)
	memory := reminder.WrapWithSource(reminder.WrapMemory("project", "project memory body"), reminder.ProviderMemoryProject)
	turn := reminder.AttachToContent("what the user typed", []string{skills, memory})

	var usage conv.ContextUsage
	addContextUsage(&usage, turn)

	if usage.Skills != ai.EstimateTokens(skills) {
		t.Errorf("Skills = %d, want %d", usage.Skills, ai.EstimateTokens(skills))
	}
	if usage.MemoryFiles != ai.EstimateTokens(memory) {
		t.Errorf("MemoryFiles = %d, want %d", usage.MemoryFiles, ai.EstimateTokens(memory))
	}
	if whole := ai.EstimateTokens(turn); usage.Messages >= whole {
		t.Errorf("Messages = %d counts the reminders again; the whole turn is only %d", usage.Messages, whole)
	}
	if usage.Messages == 0 {
		t.Error("the user's own text was dropped from Messages")
	}
}

// A one-time notice belongs to the turn that carried it, not to a category of
// its own — there is nothing the user can configure to make it smaller.
func TestAddContextUsageKeepsNoticesInMessages(t *testing.T) {
	turn := reminder.AttachToContent("what the user typed", []string{reminder.Wrap("a cancel notice")})

	var usage conv.ContextUsage
	addContextUsage(&usage, turn)

	if usage.Skills != 0 || usage.MemoryFiles != 0 {
		t.Errorf("an unsourced notice was filed as skills=%d memory=%d", usage.Skills, usage.MemoryFiles)
	}
	if want := ai.EstimateTokens(turn); usage.Messages != want {
		t.Errorf("Messages = %d, want the whole turn %d", usage.Messages, want)
	}
}

func TestMessageWireCoversEveryFieldSentToTheProvider(t *testing.T) {
	// Every block kind that goes out, in one message: the conversation is one
	// ordered sequence now, so a field that is not in it cannot be sent — and
	// DisplayContent, which this used to include, is not in it any more.
	msg := core.Message{
		Role: ai.RoleAssistant,
		Content: ai.Content{
			ai.ThinkingBlock("reasoning text", ""),
			ai.TextBlock("visible answer"),
			ai.ToolCallBlock(core.ToolCall{ID: "1", Name: "Bash", Input: `{"command":"ls"}`}),
			ai.ToolResultBlock(ai.ToolResult{ToolCallID: "1", Content: ai.TextContent("tool output")}),
		},
	}

	wire := messageWire(msg)
	for _, want := range []string{"visible answer", "reasoning text", "Bash", `{"command":"ls"}`, "tool output"} {
		if !strings.Contains(wire, want) {
			t.Errorf("wire text is missing %q: %q", want, wire)
		}
	}
	if strings.Contains(wire, "rendered for the TUI only") {
		t.Errorf("display-only content is not sent and must not be counted: %q", wire)
	}
}

func TestToolSchemaWireCoversTheWholeDefinition(t *testing.T) {
	wire := toolSchemaWire(core.ToolSchema{
		Name:        "Bash",
		Description: "run a command",
		Definition:  map[string]any{"type": "object"},
	})

	// What the estimate has to cover is the whole definition, not just the
	// prose: a dozen schemas can outweigh the conversation. The JSON key the
	// definition lands under is the encoder's business — no provider ever sees
	// this string, and each driver builds its own payload from Schema fields.
	for _, want := range []string{"Bash", "run a command", `"type":"object"`} {
		if !strings.Contains(wire, want) {
			t.Errorf("schema text is missing %q: %q", want, wire)
		}
	}
}
