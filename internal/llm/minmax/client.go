package minmax

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/v2/internal/llm"
	anthropicprovider "github.com/genai-io/san/v2/internal/llm/anthropic"
)

type Client struct {
	inner      *anthropicprovider.Client
	listModels func(context.Context) ([]llm.ModelInfo, error)
}

func NewClient(client anthropicsdk.Client, modelClient openai.Client, name string) *Client {
	return &Client{
		inner: anthropicprovider.NewClient(client, name),
		listModels: func(ctx context.Context) ([]llm.ModelInfo, error) {
			return fetchModels(ctx, modelClient)
		},
	}
}

func (c *Client) Name() string {
	return c.inner.Name()
}

func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return c.inner.Stream(ctx, opts)
}

func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	if c.listModels == nil {
		return StaticModels(), nil
	}

	models, err := c.listModels(ctx)
	if err != nil || len(models) == 0 {
		return StaticModels(), nil
	}
	return models, nil
}

var _ llm.Provider = (*Client)(nil)

func fetchModels(ctx context.Context, client openai.Client) ([]llm.ModelInfo, error) {
	page, err := client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		models = append(models, enrichModelInfo(m))
	}

	if len(models) == 0 {
		return nil, nil
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models, nil
}

func enrichModelInfo(model openai.Model) llm.ModelInfo {
	if info, ok := CatalogModel(model.ID); ok {
		return info
	}

	info := llm.ModelInfo{
		ID:          model.ID,
		Name:        model.ID,
		DisplayName: model.ID,
	}

	if raw := model.RawJSON(); raw != "" {
		var extra struct {
			ContextLength int `json:"context_length"`
		}
		if err := json.Unmarshal([]byte(raw), &extra); err == nil && extra.ContextLength > 0 {
			info.InputTokenLimit = extra.ContextLength
		}
	}

	return info
}
