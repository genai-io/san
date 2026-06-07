// Package mimo implements the Provider interface using the Mimo API.
// Mimo's API is Anthropic-compatible, so we delegate streaming to the
// anthropic provider and only override ListModels for the platform API.
package mimo

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/genai-io/san/internal/llm"
	anthropicprovider "github.com/genai-io/san/internal/llm/anthropic"
	"github.com/genai-io/san/internal/secret"
)

const modelsURL = "https://platform.xiaomimimo.com/api/v1/models"

var modelsClient = &http.Client{Timeout: 10 * time.Second}

// Client implements the Provider interface for Mimo.
// Streaming is delegated to the anthropic provider; ListModels uses the platform API.
type Client struct {
	inner *anthropicprovider.Client
}

// NewClient creates a new Mimo client wrapping an anthropic provider client.
func NewClient(client anthropicsdk.Client, name string) *Client {
	return &Client{inner: anthropicprovider.NewClient(client, name)}
}

// Name returns the provider name.
func (c *Client) Name() string { return c.inner.Name() }

// Stream delegates to the anthropic provider for full API compatibility.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return c.inner.Stream(ctx, opts)
}

// modelsResponse is the OpenRouter-style response from the Mimo models API.
type modelsResponse struct {
	Data []struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		ContextLength   int    `json:"context_length"`
		MaxOutputLength int    `json:"max_output_length"`
	} `json:"data"`
}

// ListModels fetches available models from the Mimo platform API,
// falling back to the static catalog on failure.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return StaticModels(), nil
	}
	req.Header.Set("Authorization", "Bearer "+secret.Resolve("MIMO_API_KEY"))

	resp, err := modelsClient.Do(req)
	if err != nil {
		return StaticModels(), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return StaticModels(), nil
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return StaticModels(), nil
	}

	models := make([]llm.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if info, ok := CatalogModel(m.ID); ok {
			models = append(models, info)
			continue
		}
		models = append(models, llm.ModelInfo{
			ID:               m.ID,
			Name:             m.Name,
			DisplayName:      m.Name,
			InputTokenLimit:  m.ContextLength,
			OutputTokenLimit: m.MaxOutputLength,
		})
	}

	if len(models) == 0 {
		return StaticModels(), nil
	}

	return models, nil
}

// Ensure Client implements Provider
var _ llm.Provider = (*Client)(nil)
