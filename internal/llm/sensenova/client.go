// Package sensenova implements the Provider interface for SenseNova (商汤日日新)
// using the OpenAI-compatible chat-completions endpoint.
//
// SenseNova also exposes an Anthropic Messages API-compatible endpoint, but
// that endpoint reports input_tokens=0 / output_tokens=0 in every SSE event,
// which makes context-usage tracking impossible. The OpenAI endpoint honors
// stream_options.include_usage and returns real counts in the final chunk,
// so we use it via the shared openaicompat layer.
package sensenova

import (
	"context"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openaicompat"
)

// Client implements the Provider interface for SenseNova using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new SenseNova client with the given OpenAI SDK client.
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
	})
}

// ListModels returns the static model catalog.
//
// The OpenAI /v1/models endpoint on SenseNova returns entries without
// token-limit fields, so the static catalog is the authoritative source.
// If SenseNova enriches the listing in the future, this method can fetch
// dynamically while keeping the static catalog as fallback.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return StaticModels(), nil
}

var _ llm.Provider = (*Client)(nil)
