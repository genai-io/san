// Package agnesai implements the Provider interface for Agnes-AI
// (https://agnes-ai.com). Agnes-AI exposes an OpenAI-compatible endpoint,
// so we reuse the openai-go SDK with a custom base URL and the shared
// openaicompat helpers.
package agnesai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openaicompat"
)

// Client implements the Provider interface for Agnes-AI using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new Agnes-AI client with the given OpenAI SDK client.
func NewClient(client openai.Client, name string) *Client {
	return &Client{
		client: client,
		name:   name,
	}
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

// ListModels returns the available chat-capable models for Agnes-AI using
// the /models API. Non-chat modalities (image, video) live on separate
// endpoints and are filtered out so they can't be selected for chat.
// The list is otherwise fully dynamic — no hardcoded catalog or fallback.
// If the API errors, the error propagates so users see the real failure
// rather than a stale offline list.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		id := m.ID
		if !isChatModel(id) {
			continue
		}
		info := llm.ModelInfo{ID: id, Name: id, DisplayName: id}
		if raw := m.RawJSON(); raw != "" {
			var extra struct {
				ContextLength int `json:"context_length"`
			}
			if err := json.Unmarshal([]byte(raw), &extra); err == nil && extra.ContextLength > 0 {
				info.InputTokenLimit = extra.ContextLength
			}
		}
		if info.InputTokenLimit == 0 {
			info.InputTokenLimit = staticInputLimit(id)
		}
		models = append(models, info)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("agnes-ai returned no models")
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models, nil
}

// isChatModel reports whether the given model ID is a chat-completions
// model. Agnes-AI's /v1/models endpoint returns every modality in one
// list (agnes-*-flash text, agnes-image-*, agnes-video-*); only the text
// models work with /v1/chat/completions, so the others must be filtered
// out before being surfaced in the model picker.
func isChatModel(modelID string) bool {
	m := strings.ToLower(modelID)
	return !strings.HasPrefix(m, "agnes-image") && !strings.HasPrefix(m, "agnes-video")
}

// Ensure Client implements Provider.
var _ llm.Provider = (*Client)(nil)
