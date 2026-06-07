package openai

import (
	"strings"

	"github.com/genai-io/san/internal/llm"
)

var reasoningEfforts = []string{"none", "low", "medium", "high", "xhigh"}
var highOnlyReasoningEfforts = []string{"high"}

func (c *Client) ThinkingEfforts(model string) []string {
	return openAIThinkingEfforts(model)
}

func (c *Client) DefaultThinkingEffort(model string) string {
	switch efforts := openAIThinkingEfforts(model); len(efforts) {
	case 0:
		return ""
	case 1:
		return efforts[0]
	default:
		return "medium"
	}
}

func openAIThinkingEfforts(model string) []string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(normalized, "gpt-5.5"), strings.HasPrefix(normalized, "gpt-5.4"), strings.HasPrefix(normalized, "gpt-6"):
		return reasoningEfforts
	case strings.HasPrefix(normalized, "gpt-5"), strings.HasPrefix(normalized, "o1"), strings.HasPrefix(normalized, "o3"), strings.HasPrefix(normalized, "o4"), strings.Contains(normalized, "codex"):
		return highOnlyReasoningEfforts
	default:
		return nil
	}
}

func openAIModelInfo(modelID string) llm.ModelInfo {
	return llm.ModelInfo{
		ID:          modelID,
		Name:        modelID,
		DisplayName: modelID,
	}
}
