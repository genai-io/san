// Package moonshot implements the Provider interface using the Moonshot AI platform.
// Moonshot's API is OpenAI-compatible, so we reuse the openai-go SDK with a custom base URL.
package moonshot

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openaicompat"
)

// Client implements the Provider interface for Moonshot AI using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new Moonshot client with the given OpenAI SDK client.
func NewClient(client openai.Client, name string) *Client {
	return &Client{
		client: client,
		name:   name,
	}
}

// Name returns the provider name.
func (c *Client) Name() string {
	return c.name
}

func (c *Client) ThinkingEfforts(model string) []string {
	return []string{"none", "low", "medium", "high", "xhigh"}
}

func (c *Client) DefaultThinkingEffort(model string) string {
	return "medium"
}

// convertAssistant converts an assistant message for Moonshot.
// Moonshot requires reasoning_content on all assistant messages when thinking
// is enabled — we always include the field (empty string if no thinking content).
func convertAssistant(msg core.Message) openai.ChatCompletionMessageParamUnion {
	return openaicompat.AssistantMessageWithReasoning(msg, msg.Thinking)
}

// Stream sends a completion request and returns a channel of streaming chunks.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return openaicompat.StreamChatCompletions(ctx, openaicompat.ChatStreamConfig{
		Client:           c.client,
		ProviderName:     c.name,
		Options:          opts,
		ConvertAssistant: convertAssistant,
		ConfigureParams: func(params *openai.ChatCompletionNewParams) {
			if opts.ThinkingEffort != "" && opts.ThinkingEffort != "off" && opts.ThinkingEffort != "none" {
				params.SetExtraFields(map[string]any{
					"thinking": map[string]any{"type": "enabled"},
				})
			}
		},
		ExtractReasoning: true,
	})
}

// ListModels returns the available models for Moonshot AI using the API.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		id := m.ID
		info := llm.ModelInfo{ID: id, Name: id, DisplayName: id}
		if raw := m.RawJSON(); raw != "" {
			var extra struct {
				ContextLength   int `json:"context_length"`
				MaxOutputTokens int `json:"max_output_tokens"`
			}
			if err := json.Unmarshal([]byte(raw), &extra); err == nil {
				info.InputTokenLimit = extra.ContextLength
				info.OutputTokenLimit = extra.MaxOutputTokens
			}
		}
		if info.InputTokenLimit == 0 {
			info.InputTokenLimit = staticInputLimit(id)
		}
		if info.OutputTokenLimit == 0 {
			info.OutputTokenLimit = staticOutputLimit(id)
		}
		models = append(models, info)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("moonshot returned no models")
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models, nil
}

// Ensure Client implements Provider
var _ llm.Provider = (*Client)(nil)
