package llm

import (
	"slices"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// RequestMessages adapts input to the model's capabilities. The native
// conversation, including images inside tool results, remains unchanged.
func (l *Client) RequestMessages(messages []core.Message) []core.Message {
	if SupportsImages(l.provider, l.model) {
		return messages
	}
	filtered := slices.Clone(messages)
	for i := range filtered {
		filtered[i].Content = textOnlyContent(filtered[i].Content)
	}
	return filtered
}

func textOnlyContent(content ai.Content) ai.Content {
	filtered := make(ai.Content, 0, len(content))
	for _, block := range content {
		if block.Type == ai.BlockImage {
			continue
		}
		if block.ToolResult != nil {
			result := *block.ToolResult
			result.Content = textOnlyContent(result.Content)
			block.ToolResult = &result
		}
		filtered = append(filtered, block)
	}
	return filtered
}
