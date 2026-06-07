package moonshot

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

// APIKeyMeta is the metadata for Moonshot via API Key
var APIKeyMeta = llm.Meta{
	Provider:    llm.Moonshot,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"MOONSHOT_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new Moonshot client using API Key authentication.
// The Moonshot API is OpenAI-compatible, so we use the OpenAI SDK with a custom base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("MOONSHOT_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.moonshot.cn/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("MOONSHOT_API_KEY")),
		option.WithBaseURL(baseURL),
	)
	return NewClient(client, "moonshot:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
