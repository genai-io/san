// Package deepseek implements the Provider interface using the DeepSeek API.
// DeepSeek's API is OpenAI-compatible, so we reuse the openai-go SDK with a custom base URL.
package deepseek

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/llm/openaicompat"
)

// Client implements the Provider interface for DeepSeek using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new DeepSeek client with the given OpenAI SDK client.
func NewClient(client openai.Client, name string) *Client {
	return &Client{client: client, name: name}
}

// Name returns the provider name.
func (c *Client) Name() string { return c.name }

// isReasonerModel returns true if the model supports thinking/reasoning mode.
func isReasonerModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "reasoner")
}

// makeAssistantConverter returns a provider-specific assistant message converter.
func makeAssistantConverter(thinking bool) func(core.Message) openai.ChatCompletionMessageParamUnion {
	if !thinking {
		return openaicompat.DefaultAssistantMessage
	}
	return func(msg core.Message) openai.ChatCompletionMessageParamUnion {
		return openaicompat.AssistantMessageWithReasoning(msg, msg.Thinking)
	}
}

func (c *Client) ThinkingEfforts(model string) []string {
	if !isReasonerModel(model) {
		return nil
	}
	return []string{"off", "think"}
}

func (c *Client) DefaultThinkingEffort(model string) string {
	if !isReasonerModel(model) {
		return ""
	}
	return "off"
}

// Stream sends a completion request and returns a channel of streaming chunks.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	thinking := isReasonerModel(opts.Model)
	return openaicompat.StreamChatCompletions(ctx, openaicompat.ChatStreamConfig{
		Client:           c.client,
		ProviderName:     c.name,
		Options:          opts,
		ConvertAssistant: makeAssistantConverter(thinking),
		ExtractReasoning: thinking,
	})
}

// ListModels returns the available models from the DeepSeek API, falling back to the static catalog.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return StaticModels(), nil
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		if info, ok := CatalogModel(m.ID); ok {
			models = append(models, info)
			continue
		}
		info := llm.ModelInfo{ID: m.ID, Name: m.ID, DisplayName: m.ID}
		if raw := m.RawJSON(); raw != "" {
			var extra struct {
				ContextLength int `json:"context_length"`
			}
			if err := json.Unmarshal([]byte(raw), &extra); err == nil && extra.ContextLength > 0 {
				info.InputTokenLimit = extra.ContextLength
			}
		}
		models = append(models, info)
	}

	if len(models) == 0 {
		return StaticModels(), nil
	}

	return models, nil
}

// Ensure Client implements Provider
var _ llm.Provider = (*Client)(nil)
var _ llm.ThinkingEffortProvider = (*Client)(nil)
