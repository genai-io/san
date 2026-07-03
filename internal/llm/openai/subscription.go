package openai

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"slices"
	"time"

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

// modelsFetchTimeout bounds the live catalog request so a slow/blocked fetch
// doesn't wedge the connect flow before falling back to the static list.
const modelsFetchTimeout = 8 * time.Second

// staticSubscriptionModels is the fallback catalog used when the live
// ChatGPT Codex model list can't be fetched.
var staticSubscriptionModels = []string{
	"gpt-5.1-codex",
	"gpt-5.1-codex-mini",
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

// subscriptionCatalog returns the ChatGPT Codex model catalog. It fetches the
// live list the backend advertises for this account, falling back to a static
// list when the fetch fails or returns nothing (offline, not signed in, or an
// unexpected response shape) so the connect flow never breaks.
func (c *Client) subscriptionCatalog(ctx context.Context) []llm.ModelInfo {
	var resp codexModelsResponse
	err := c.client.Get(ctx, "models", nil, &resp, option.WithRequestTimeout(modelsFetchTimeout))
	if err == nil {
		if models := resp.toModelInfos(); len(models) > 0 {
			return models
		}
	}
	return staticSubscriptionCatalog()
}

// staticSubscriptionCatalog builds the fallback catalog from the static slugs.
func staticSubscriptionCatalog() []llm.ModelInfo {
	models := make([]llm.ModelInfo, 0, len(staticSubscriptionModels))
	for _, id := range staticSubscriptionModels {
		models = append(models, openAIModelInfo(id))
	}
	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models
}

// codexModelsResponse is the ChatGPT Codex /models catalog. The backend wraps the
// list under "data" (OpenAI convention); "models" is accepted defensively.
type codexModelsResponse struct {
	Data   []codexModel `json:"data"`
	Models []codexModel `json:"models"`
}

// codexModel is one catalog entry. Field names span the observed variants: the
// request slug lives under "model" (with "slug"/"id" as fallbacks), and
// show_in_picker hides entries the account shouldn't select.
type codexModel struct {
	ID           string `json:"id"`
	Model        string `json:"model"`
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	ShowInPicker *bool  `json:"show_in_picker"`
}

// slug returns the model id to send in requests, preferring the explicit slug.
func (m codexModel) slug() string {
	switch {
	case m.Model != "":
		return m.Model
	case m.Slug != "":
		return m.Slug
	default:
		return m.ID
	}
}

// toModelInfos converts the catalog to llm.ModelInfo, dropping picker-hidden and
// duplicate entries and filling token limits from the known OpenAI specs.
func (r codexModelsResponse) toModelInfos() []llm.ModelInfo {
	entries := r.Data
	if len(entries) == 0 {
		entries = r.Models
	}

	seen := make(map[string]bool, len(entries))
	models := make([]llm.ModelInfo, 0, len(entries))
	for _, m := range entries {
		if m.ShowInPicker != nil && !*m.ShowInPicker {
			continue
		}
		id := m.slug()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		info := openAIModelInfo(id)
		if m.DisplayName != "" {
			info.Name = m.DisplayName
			info.DisplayName = m.DisplayName
		}
		models = append(models, info)
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

// subscriptionAuthenticator adapts the ChatGPT OAuth flow to llm.Authenticator
// so the app layer can trigger sign-in/out through the llm facade rather than
// importing this provider package directly.
type subscriptionAuthenticator struct{}

func (subscriptionAuthenticator) Login(ctx context.Context, onURL func(string)) error {
	_, err := oauth.Login(ctx, onURL)
	return err
}

func (subscriptionAuthenticator) Logout() error { return oauth.Logout() }

func init() {
	llm.Register(SubscriptionMeta, NewSubscriptionClient)
	llm.RegisterAuthenticator(llm.OpenAI, llm.AuthSubscription, subscriptionAuthenticator{})
}
