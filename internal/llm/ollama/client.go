// Package ollama implements the Provider interface for Ollama.
// Ollama speaks an OpenAI-compatible Chat Completions API, so we reuse the
// openaicompat helpers with a local base URL and no API key.
package ollama

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/llm/openaicompat"
)

// Client implements the Provider interface for Ollama using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new Ollama client with the given OpenAI SDK client.
func NewClient(client openai.Client, name string) *Client {
	return &Client{client: client, name: name}
}

// Name returns the provider name.
func (c *Client) Name() string { return c.name }

// Stream sends a completion request and returns a channel of streaming chunks.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return openaicompat.StreamChatCompletions(ctx, openaicompat.ChatStreamConfig{
		Client:           c.client,
		ProviderName:     c.name,
		Options:          opts,
		ConvertAssistant: openaicompat.DefaultAssistantMessage,
		// Ollama's OpenAI-compatible endpoint doesn't expose reasoning
		// content natively — we keep extraction disabled.
		ExtractReasoning: false,
	})
}

// ListModels returns the available models from the Ollama API.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
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
