// Package sensenova implements the Provider interface for SenseNova (商汤日日新).
// SenseNova exposes an Anthropic Messages API-compatible endpoint, so we reuse
// the Anthropic SDK with a custom base URL.
package sensenova

import (
	"context"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/genai-io/san/internal/llm"
	anthropicprovider "github.com/genai-io/san/internal/llm/anthropic"
)

// Client wraps the Anthropic provider client for SenseNova's Messages API.
type Client struct {
	inner *anthropicprovider.Client
}

// NewClient creates a new SenseNova client wrapping the given Anthropic SDK client.
func NewClient(client anthropicsdk.Client, name string) *Client {
	return &Client{
		inner: anthropicprovider.NewClient(client, name),
	}
}

func (c *Client) Name() string { return c.inner.Name() }

func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return c.inner.Stream(ctx, opts)
}

// ListModels returns the static model catalog.
// Delegating to the inner Anthropic client is not appropriate because:
//  1. SenseNova may not expose a /v1/models endpoint.
//  2. On failure, the Anthropic client falls back to Anthropic's own static models,
//     which are not SenseNova models.
//
// If SenseNova adds a model listing API in the future, this method can be updated
// to fetch dynamically while keeping the static catalog as fallback.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return StaticModels(), nil
}

var _ llm.Provider = (*Client)(nil)
