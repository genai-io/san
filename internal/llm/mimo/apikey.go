package mimo

import (
	"context"
	"os"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/v2/internal/llm"
	"github.com/genai-io/san/v2/internal/secret"
)

// APIKeyMeta is the metadata for Mimo via API Key
var APIKeyMeta = llm.Meta{
	Provider:    llm.Mimo,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     []string{"MIMO_API_KEY"},
	DisplayName: "Direct API",
}

// NewAPIKeyClient creates a new Mimo client using API Key authentication.
// The Mimo API is Anthropic-compatible. Base URL is read from MIMO_BASE_URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := os.Getenv("MIMO_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.xiaomimimo.com/anthropic"
	}

	client := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey(secret.Resolve("MIMO_API_KEY")),
		anthropicoption.WithBaseURL(baseURL),
	)
	return NewClient(client, "mimo:api_key"), nil
}

// init registers the API Key provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
