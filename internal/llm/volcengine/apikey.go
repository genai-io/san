package volcengine

import (
	"context"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

const defaultBaseURL = "https://ark.cn-beijing.volces.com/api/coding"

var APIKeyMeta = llm.Meta{
	Provider:    llm.Volcengine,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"VOLCENGINE_API_KEY"},
	DisplayName: "Bearer Token API",
}

func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("VOLCENGINE_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	apiKey := secret.Resolve("VOLCENGINE_API_KEY")
	client := anthropicsdk.NewClient(
		anthropicoption.WithAuthToken(apiKey),
		anthropicoption.WithBaseURL(baseURL),
	)
	return NewClientWithConfig(client, "volcengine:api_key", baseURL, apiKey), nil
}

func init() {
	llm.RegisterProviderDisplay(llm.Volcengine, llm.ProviderDisplay{Name: "Volcengine Ark (火山引擎)", Order: 120})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
