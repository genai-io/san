package bigmodel

import (
	"context"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

// APIKeyMeta is the metadata for BigModel (Zhipu / Z.ai — GLM series) via API Key.
var APIKeyMeta = llm.Meta{
	Provider:    llm.BigModel,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"BIGMODEL_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new BigModel client using API Key authentication.
// BigModel publishes an OpenAI-compatible endpoint, so we use the OpenAI SDK
// with a custom base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := os.Getenv("BIGMODEL_BASE_URL")
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/paas/v4"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("BIGMODEL_API_KEY")),
		option.WithBaseURL(baseURL),
	)
	return NewClient(client, "bigmodel:api_key"), nil
}

func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
