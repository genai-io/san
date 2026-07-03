package openai

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

// newSubscriptionTestClient reuses the capturing transport but flips the client
// into ChatGPT Codex (subscription) mode, bypassing the OAuth middleware so the
// request-shaping behavior can be tested in isolation.
func newSubscriptionTestClient(t *captureStreamingTransport) *Client {
	c := newTestClient(t)
	c.subscription = true
	return c
}

func TestSubscriptionStreamIsStatelessWithEncryptedReasoning(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newSubscriptionTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:          "gpt-5-codex",
		Messages:       []core.Message{{Role: core.RoleUser, Content: "hi"}},
		ThinkingEffort: "high",
	}))

	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}

	store, ok := payload["store"].(bool)
	if !ok || store {
		t.Fatalf("expected store=false in subscription request, got %#v", payload["store"])
	}

	include, ok := payload["include"].([]any)
	if !ok || !slices.Contains(include, "reasoning.encrypted_content") {
		t.Fatalf("expected include to contain reasoning.encrypted_content, got %#v", payload["include"])
	}
}

func TestNonSubscriptionStreamOmitsStore(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:    "gpt-5.4",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	}))

	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}
	if _, present := payload["store"]; present {
		t.Fatalf("direct-API request should not set store, got %#v", payload["store"])
	}
	if _, present := payload["include"]; present {
		t.Fatalf("direct-API request should not set include, got %#v", payload["include"])
	}
}

func TestSubscriptionProviderRegistered(t *testing.T) {
	meta, ok := llm.GetMeta(llm.OpenAI, llm.AuthSubscription)
	if !ok {
		t.Fatal("subscription provider is not registered")
	}
	if meta.DisplayName != "ChatGPT Subscription" {
		t.Errorf("DisplayName = %q, want ChatGPT Subscription", meta.DisplayName)
	}
	if len(meta.EnvVars) != 0 {
		t.Errorf("subscription auth should declare no env vars, got %v", meta.EnvVars)
	}
	if !llm.SupportsInteractiveLogin(llm.OpenAI, llm.AuthSubscription) {
		t.Error("subscription auth should register an interactive authenticator")
	}

	// The factory must build a working client without credentials or network:
	// the model list is static and no request is issued until a stream call.
	p, err := llm.GetProvider(context.Background(), llm.OpenAI, llm.AuthSubscription)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.Name() != "openai:subscription" {
		t.Errorf("provider name = %q, want openai:subscription", p.Name())
	}
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) == 0 {
		t.Fatalf("ListModels = %v, %v; want a non-empty static catalog", models, err)
	}
}

func TestSubscriptionCatalog(t *testing.T) {
	client := newSubscriptionTestClient(&captureStreamingTransport{})

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != len(subscriptionModels) {
		t.Fatalf("got %d models, want %d", len(models), len(subscriptionModels))
	}

	idx := slices.IndexFunc(models, func(m llm.ModelInfo) bool { return m.ID == "gpt-5-codex" })
	if idx < 0 {
		t.Fatalf("expected gpt-5-codex in catalog, got %v", models)
	}
	if models[idx].InputTokenLimit == 0 {
		t.Errorf("expected a non-zero context window for gpt-5-codex")
	}
}
