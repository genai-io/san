package minmax

import (
	"context"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

var APIKeyMeta = llm.Meta{
	Provider:    llm.MinMax,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"MINIMAX_API_KEY"},
	DisplayName: "Direct API",
}

func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("MINIMAX_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.minimaxi.com/anthropic"
	}
	openAIBaseURL := secret.Resolve("MINIMAX_OPENAI_BASE_URL")
	if openAIBaseURL == "" {
		openAIBaseURL = "https://api.minimaxi.com/v1"
	}
	apiKey := secret.Resolve("MINIMAX_API_KEY")

	client := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
	)
	modelClient := openai.NewClient(
		openaioption.WithAPIKey(apiKey),
		openaioption.WithBaseURL(openAIBaseURL),
		openaioption.WithMaxRetries(0),
	)
	return NewClient(client, modelClient, "minmax:api_key"), nil
}

func init() {
	llm.RegisterProviderDisplay(llm.MinMax, llm.ProviderDisplay{Name: "MiniMax", Order: 60})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
	llm.RegisterCostEstimator(llm.MinMax, EstimateCost)
}
