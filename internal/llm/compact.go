package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
)

// CompactSummary generates only the raw summary; callers retain their own
// recording scope and decide which context to attach to the result.
func (c *Client) CompactSummary(ctx context.Context, msgs []core.Message, focus string, admission CompletionAdmission) (string, error) {
	text := core.BuildCompactionText(msgs)
	if focus != "" {
		text += "\n\n**Important**: Focus the summary on: " + focus
	}
	resp, err := c.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(text, nil)}, nil, core.CompactMaxTokens, admission)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}
	summary := strings.TrimSpace(resp.Content.Text())
	if summary == "" {
		return "", fmt.Errorf("compaction produced empty summary")
	}
	return summary, nil
}
