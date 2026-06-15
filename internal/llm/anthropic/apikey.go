package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/internal/llm"
)

// APIKeyMeta is the metadata for Anthropic via API Key
var APIKeyMeta = llm.Meta{
	Provider:    llm.Anthropic,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"ANTHROPIC_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new Anthropic client using API Key authentication
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	// Retries are owned by the app-level decorator; disable the SDK's own.
	client := anthropic.NewClient(option.WithMaxRetries(0))
	return NewClient(client, "anthropic:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.RegisterProviderDisplay(llm.Anthropic, llm.ProviderDisplay{Name: "Anthropic", Order: 10})
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
