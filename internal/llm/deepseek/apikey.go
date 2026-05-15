package deepseek

import (
	"context"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/secret"
)

// APIKeyMeta is the metadata for DeepSeek via API Key
var APIKeyMeta = llm.Meta{
	Provider:    llm.DeepSeek,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"DEEPSEEK_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new DeepSeek client using API Key authentication.
// The DeepSeek API is OpenAI-compatible, so we use the OpenAI SDK with a custom base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("DEEPSEEK_API_KEY")),
		option.WithBaseURL(baseURL),
	)
	return NewClient(client, "deepseek:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
