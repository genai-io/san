package agnesai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

// APIKeyMeta is the metadata for Agnes-AI via API Key.
var APIKeyMeta = llm.Meta{
	Provider:    llm.AgnesAI,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"AGNESAI_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new Agnes-AI client using API Key authentication.
// Agnes-AI publishes an OpenAI-compatible endpoint, so we use the OpenAI SDK
// with a custom base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("AGNESAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://apihub.agnes-ai.com/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("AGNESAI_API_KEY")),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(0),
	)
	return NewClient(client, "agnesai:api_key"), nil
}

func init() {
	llm.RegisterProviderDisplay(llm.AgnesAI, llm.ProviderDisplay{Name: "Agnes-AI", Order: 130})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
