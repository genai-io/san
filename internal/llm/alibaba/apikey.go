package alibaba

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/v2/internal/llm"
	"github.com/genai-io/san/v2/internal/secret"
)

// APIKeyMeta is the metadata for Qwen via API Key (DashScope)
var APIKeyMeta = llm.Meta{
	Provider:    llm.Alibaba,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"DASHSCOPE_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new Qwen client using API Key authentication.
// The DashScope API is OpenAI-compatible, so we use the OpenAI SDK with a custom base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("DASHSCOPE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("DASHSCOPE_API_KEY")),
		option.WithBaseURL(baseURL),
	)
	return NewClient(client, "alibaba:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
