package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/genai-io/san/v2/internal/llm"
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
	client := anthropic.NewClient()
	return NewClient(client, "anthropic:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
