package openai

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openai/oauth"
)

// codexBaseURL is the ChatGPT subscription (Codex) Responses endpoint root. The
// trailing slash is required: the SDK resolves the "responses" path against it,
// and without the slash the final "codex" segment would be dropped, pointing the
// request at the wrong endpoint.
const codexBaseURL = "https://chatgpt.com/backend-api/codex/"

// subscriptionModels lists the models reachable through the ChatGPT Codex
// backend. That backend exposes no /models endpoint, so the catalog is static.
var subscriptionModels = []string{
	"gpt-5.1-codex",
	"gpt-5.1",
	"gpt-5-codex",
	"gpt-5",
}

// SubscriptionMeta is the metadata for OpenAI via a ChatGPT subscription (OAuth).
// It has no EnvVars because it authenticates with an OAuth login, not a key.
var SubscriptionMeta = llm.Meta{
	Provider:    llm.OpenAI,
	AuthMethod:  llm.AuthSubscription,
	EnvVars:     nil,
	DisplayName: "ChatGPT Subscription",
}

// NewSubscriptionClient creates an OpenAI client that talks to the ChatGPT Codex
// backend using a subscription OAuth token instead of an API key. The bearer
// token and account id are injected per request from a refreshing TokenSource,
// so a long session survives token expiry transparently.
func NewSubscriptionClient(ctx context.Context) (llm.Provider, error) {
	tokens := oauth.NewTokenSource()
	sessionID := newSessionID()

	sdk := openai.NewClient(
		option.WithBaseURL(codexBaseURL),
		option.WithMaxRetries(0),
		option.WithHeader("OpenAI-Beta", "responses=experimental"),
		option.WithHeader("originator", "codex_cli_rs"),
		option.WithHeader("session_id", sessionID),
		option.WithHeader("User-Agent", "codex_cli_rs"),
		option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			access, accountID, err := tokens.Token(req.Context())
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+access)
			req.Header.Set("chatgpt-account-id", accountID)
			return next(req)
		}),
	)

	c := NewClient(sdk, "openai:subscription")
	c.subscription = true
	return c, nil
}

// subscriptionCatalog returns the static model catalog for the ChatGPT Codex
// backend, sorted by id for a stable UI ordering.
func subscriptionCatalog() []llm.ModelInfo {
	models := make([]llm.ModelInfo, 0, len(subscriptionModels))
	for _, id := range subscriptionModels {
		models = append(models, openAIModelInfo(id))
	}
	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models
}

// newSessionID returns a random UUIDv4 for the per-session `session_id` header.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func init() {
	llm.Register(SubscriptionMeta, NewSubscriptionClient)
}
