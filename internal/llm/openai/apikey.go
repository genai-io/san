package openai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
)

// APIKeyMeta is the metadata for OpenAI via API Key
var APIKeyMeta = llm.Meta{
	Provider:    llm.OpenAI,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"OPENAI_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new OpenAI client using API Key authentication
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	// Retries are owned by the app-level decorator; disable the SDK's own.
	client := openai.NewClient(option.WithMaxRetries(0))
	return NewClient(client, "openai:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.RegisterProviderDisplay(llm.OpenAI, llm.ProviderDisplay{Name: "OpenAI", Order: 20})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
