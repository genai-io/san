package minmax

import (
	"context"
	"os"

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
	baseURL := os.Getenv("MINIMAX_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.minimaxi.com/anthropic"
	}
	openAIBaseURL := os.Getenv("MINIMAX_OPENAI_BASE_URL")
	if openAIBaseURL == "" {
		openAIBaseURL = "https://api.minimaxi.com/v1"
	}
	apiKey := secret.Resolve("MINIMAX_API_KEY")

	client := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithBaseURL(baseURL),
	)
	modelClient := openai.NewClient(
		openaioption.WithAPIKey(apiKey),
		openaioption.WithBaseURL(openAIBaseURL),
	)
	return NewClient(client, modelClient, "minmax:api_key"), nil
}

func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
