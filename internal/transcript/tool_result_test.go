package transcript

import (
	"fmt"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestHydrateToolResultNodes(t *testing.T) {
	sessionID := "session-1"
	preview := "partial output\n\n[Full output persisted to blobs/tool-result/session-1/tc-1]"
	nodes := []MessageNode{MessageNode{Message: ai.Message{ID: "m1", Role: "user", Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "tc-1", Content: ai.Content{ai.TextBlock(preview)}})}}}}

	HydrateToolResultNodes(sessionID, nodes, func(toolCallID string) (string, error) {
		if toolCallID != "tc-1" {
			return "", fmt.Errorf("unexpected toolCallID %q", toolCallID)
		}
		return "full output", nil
	})

	got := nodes[0].Content[0].ToolResult.Content[0].Text
	if got != "full output" {
		t.Fatalf("hydrated text = %q, want %q", got, "full output")
	}
}

func TestHydrateToolResultNodesIgnoresUnmatchedMarkers(t *testing.T) {
	nodes := []MessageNode{MessageNode{Message: ai.Message{ID: "m1", Role: "user", Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "tc-1", Content: ai.Content{ai.TextBlock("preview only")}})}}}}

	HydrateToolResultNodes("session-1", nodes, func(toolCallID string) (string, error) {
		t.Fatalf("unexpected load call for %q", toolCallID)
		return "", nil
	})

	got := nodes[0].Content[0].ToolResult.Content[0].Text
	if got != "preview only" {
		t.Fatalf("text changed unexpectedly: %q", got)
	}
}
