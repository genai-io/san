package minmax

import (
	"context"
	"errors"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/llm"
)

func TestListModelsReturnsStaticModels(t *testing.T) {
	c := NewClient(anthropicsdk.NewClient(), openai.NewClient(), "minmax:api_key")
	c.listModels = func(context.Context) ([]llm.ModelInfo, error) {
		return nil, errors.New("boom")
	}
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected static models")
	}
	if models[0].OutputTokenLimit != 8192 {
		t.Fatalf("expected output limit 8192, got %d", models[0].OutputTokenLimit)
	}
}

func TestListModelsReturnsDynamicModels(t *testing.T) {
	c := NewClient(anthropicsdk.NewClient(), openai.NewClient(), "minmax:api_key")
	c.listModels = func(context.Context) ([]llm.ModelInfo, error) {
		return []llm.ModelInfo{
			{ID: "zzz", Name: "zzz", DisplayName: "zzz"},
			{ID: "MiniMax-M2.7", Name: "MiniMax-M2.7", DisplayName: "MiniMax-M2.7", InputTokenLimit: 204800, OutputTokenLimit: 8192},
		}, nil
	}

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "zzz" && models[1].ID != "zzz" {
		t.Fatalf("expected dynamic model list, got %#v", models)
	}
}

func TestEstimateCost(t *testing.T) {
	cost, ok := EstimateCost("MiniMax-M2.7", llm.Usage{
		InputTokens:              1000000,
		OutputTokens:             1000000,
		CacheCreationInputTokens: 1000000,
		CacheReadInputTokens:     1000000,
	})
	if !ok {
		t.Fatal("expected pricing lookup to succeed")
	}
	if cost.Amount != 13.545 {
		t.Fatalf("expected 13.545, got %.6f", cost.Amount)
	}
	if cost.Currency != llm.CurrencyCNY {
		t.Fatalf("expected CNY, got %s", cost.Currency)
	}
}
