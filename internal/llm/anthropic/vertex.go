package anthropic

import (
	"context"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/vertex"

	"github.com/genai-io/san/internal/llm"
)

// VertexMeta is the metadata for Anthropic via Vertex AI
var VertexMeta = llm.Meta{
	Provider:    llm.Anthropic,
	AuthMethod:  llm.AuthVertex,
	EnvVars:     []string{"CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID"},
	DisplayName: "Vertex AI",
}

// vertexModels is the static list of Claude models available on Vertex AI.
//
// Source:
// - https://docs.anthropic.com/en/docs/about-claude/models/overview
// - https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude
//
// Note: Vertex AI does not provide a stable Anthropic Models API, so we use a
// static list and refresh it when Anthropic/Vertex documentation changes.
var vertexModels = []llm.ModelInfo{
	newVertexModel("claude-opus-4-7", "Claude Opus 4.7", "Claude Opus 4.7 (Most Capable)"),
	newVertexModel("claude-opus-4-6[1m]", "Claude Opus 4.6 (1M)", "Claude Opus 4.6 (1M Context)"),
	newVertexModel("claude-opus-4-6", "Claude Opus 4.6", "Claude Opus 4.6"),
	newVertexModel("claude-opus-4-5", "Claude Opus 4.5", "Claude Opus 4.5"),
	newVertexModel("claude-opus-4-1@20250805", "Claude Opus 4.1", "Claude Opus 4.1"),
	newVertexModel("claude-opus-4@20250514", "Claude Opus 4", "Claude Opus 4"),
	newVertexModel("claude-sonnet-4-6", "Claude Sonnet 4.6", "Claude Sonnet 4.6"),
	newVertexModel("claude-sonnet-4-5", "Claude Sonnet 4.5", "Claude Sonnet 4.5"),
	newVertexModel("claude-sonnet-4@20250514", "Claude Sonnet 4", "Claude Sonnet 4"),
	newVertexModel("claude-3-7-sonnet@20250219", "Claude Sonnet 3.7", "Claude Sonnet 3.7"),
	newVertexModel("claude-haiku-4-5", "Claude Haiku 4.5", "Claude Haiku 4.5 (Fast)"),
	newVertexModel("claude-3-5-haiku@20241022", "Claude Haiku 3.5", "Claude Haiku 3.5"),
}

// VertexClient wraps the standard Client with Vertex-specific behavior
type VertexClient struct {
	*Client
}

// ListModels tries the Anthropic Models API first, falling back to a static
// list with a warning error when the API is unavailable (e.g. 404 on Vertex AI).
// A failed fetch does not permanently cache the fallback — subsequent calls retry.
func (c *VertexClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	c.modelsMu.Lock()
	defer c.modelsMu.Unlock()

	if c.cachedModels != nil {
		return c.cachedModels, nil
	}

	models, err := c.fetchModels(ctx)
	if err == nil {
		c.cachedModels = models
		return c.cachedModels, nil
	}
	// Return static fallback but don't cache it so we retry next time
	return vertexModels, nil
}

// NewVertexClient creates a new Anthropic client using Vertex AI authentication
func NewVertexClient(ctx context.Context) (llm.Provider, error) {
	region := os.Getenv("CLOUD_ML_REGION")
	if region == "" {
		region = "us-east5"
	}
	projectID := os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID")

	client := anthropic.NewClient(
		vertex.WithGoogleAuth(ctx, region, projectID),
	)

	baseClient := NewClient(client, "anthropic:vertex")
	return &VertexClient{Client: baseClient}, nil
}

// Ensure VertexClient implements Provider
var _ llm.Provider = (*VertexClient)(nil)

// init registers the Vertex AI provider
func init() {
	llm.Register(VertexMeta, NewVertexClient)
}

func newVertexModel(id, name, displayName string) llm.ModelInfo {
	info, ok := CatalogModel(id)
	if !ok {
		return llm.ModelInfo{ID: id, Name: name, DisplayName: displayName}
	}
	info.Name = name
	info.DisplayName = displayName
	return info
}
