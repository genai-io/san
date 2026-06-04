package ollama

import (
	"context"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/gen-code/internal/llm"
)

// APIKeyMeta is the metadata for Ollama (keyless / local).
// Ollama does not require an API key; we pass a placeholder so the
// OpenAI-compatible SDK still sends Authorization: Bearer (which Ollama
// ignores).
var APIKeyMeta = llm.Meta{
	Provider:    llm.Ollama,
	AuthMethod:  llm.AuthAPIKey,
	EnvVars:     nil,
	DisplayName: "Local (Ollama)",
}

// NewAPIKeyClient creates a new Ollama client. Ollama speaks the OpenAI
// Chat Completions API, so we reuse the OpenAI SDK with a local base URL.
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	// Normalize: ensure the URL ends with /v1 (Ollama serves the
	// OpenAI-compatible API at /v1/chat/completions).
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}

	// Ollama ignores the Authorization header; "ollama" is a placeholder.
	client := openai.NewClient(
		option.WithAPIKey("ollama"),
		option.WithBaseURL(baseURL),
	)
	return NewClient(client, "ollama:api_key"), nil
}

// init registers the Ollama provider
func init() {
	llm.Register(APIKeyMeta, NewAPIKeyClient)
}
