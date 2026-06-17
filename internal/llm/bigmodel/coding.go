package bigmodel

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

// CodingMeta is the metadata for BigModel (Zhipu / Z.ai — GLM series) via Coding Plan.
// Reuses BIGMODEL_API_KEY; the Coding Plan only differs from Direct API in the endpoint.
var CodingMeta = llm.Meta{
	Provider:    llm.BigModel,
	AuthMethod:  llm.AuthCoding,
	EnvVars:     []string{"BIGMODEL_API_KEY"},
	DisplayName: "Coding Plan",
}

// NewCodingClient creates a new BigModel client using the Coding Plan endpoint.
// Same key as Direct API; users just pick "Coding Plan" in the UI.
func NewCodingClient(ctx context.Context) (llm.Provider, error) {
	baseURL := secret.Resolve("BIGMODEL_CODING_BASE_URL")
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	}

	client := openai.NewClient(
		option.WithAPIKey(secret.Resolve("BIGMODEL_API_KEY")),
		option.WithBaseURL(baseURL),
		option.WithMaxRetries(0),
	)
	return NewClient(client, "bigmodel:coding"), nil
}

func init() {
	llm.Register(CodingMeta, NewCodingClient)
}
