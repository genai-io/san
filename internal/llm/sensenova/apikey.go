package sensenova

import (
	"context"
	"os"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

var APIKeyMeta = llm.Meta{
	Provider:    llm.SenseNova,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"SENSENOVA_API_KEY"},
	DisplayName: "Bearer Token API",
}

func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := os.Getenv("SENSENOVA_BASE_URL")
	if baseURL == "" {
		baseURL = "https://token.sensenova.cn"
	}

	client := anthropicsdk.NewClient(
		anthropicoption.WithAuthToken(secret.Resolve("SENSENOVA_API_KEY")),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
	)
	return NewClient(client, "sensenova:api_key"), nil
}

func init() {
	llm.RegisterProviderDisplay(llm.SenseNova, llm.ProviderDisplay{Name: "SenseNova (商汤)", Order: 50})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
