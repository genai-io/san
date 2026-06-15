package sensenova

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

var APIKeyMeta = llm.Meta{
	Provider:    llm.SenseNova,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"SENSENOVA_API_KEY"},
	DisplayName: "Bearer Token API",
}

// NewAPIKeyClient creates a SenseNova client using the OpenAI-compatible
// chat-completions endpoint. SenseNova also exposes an Anthropic-compatible
// endpoint, but as of 2026-06 that endpoint reports input_tokens=0 and
// output_tokens=0 in every SSE event, making context-usage tracking
// impossible. The OpenAI endpoint honors stream_options.include_usage and
// returns real counts in the final chunk.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("SENSENOVA_BASE_URL")
	if baseURL == "" {
		baseURL = "https://token.sensenova.cn/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("SENSENOVA_API_KEY")),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(0),
	)
	return NewClient(client, "sensenova:api_key"), nil
}

func init() {
	llm.RegisterProviderDisplay(llm.SenseNova, llm.ProviderDisplay{Name: "SenseNova (商汤)", Order: 50})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
