package tui

import (
	"context"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type staticTestProvider struct{ client *ai.Client }

func (p staticTestProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}
func (staticTestProvider) Name() string { return "static-test" }
func (staticTestProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "test", ContextWindow: 200000}}, nil
}
