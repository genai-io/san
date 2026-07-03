package openai

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
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
		MaxTokens:      8192,
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

	if _, present := payload["max_output_tokens"]; present {
		t.Fatalf("subscription request must omit max_output_tokens; ChatGPT Codex rejects it, got %#v", payload["max_output_tokens"])
	}
}

func TestNonSubscriptionStreamOmitsStore(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:     "gpt-5.4",
		Messages:  []core.Message{{Role: core.RoleUser, Content: "hi"}},
		MaxTokens: 8192,
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
	if got, ok := payload["max_output_tokens"].(float64); !ok || got != 8192 {
		t.Fatalf("direct-API request should set max_output_tokens=8192, got %#v", payload["max_output_tokens"])
	}
}

// reasoningStreamBody is a Responses SSE stream whose completed response carries
// a reasoning output item with encrypted content, as the ChatGPT backend returns
// when include=[reasoning.encrypted_content].
const reasoningStreamBody = "" +
	"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":1,\"content_index\":0,\"delta\":\"ok\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_9\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"thought\"}],\"encrypted_content\":\"enc-xyz\"}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2,\"output_tokens_details\":{\"reasoning_tokens\":1}}}}\n\n" +
	"data: [DONE]\n\n"

// modelsCatalogBody is a sample ChatGPT Codex /models JSON response (real shape:
// a "models" array of {slug, display_name, context_window, ...}): two picker
// models plus one hidden entry that must be filtered out.
const modelsCatalogBody = `{"models":[` +
	`{"slug":"gpt-5.5","display_name":"GPT-5.5","context_window":272000},` +
	`{"slug":"gpt-5.4","display_name":"GPT-5.4","context_window":272000},` +
	`{"slug":"gpt-hidden","display_name":"Hidden","show_in_picker":false}` +
	`]}`

func TestSubscriptionEchoesReasoningBeforeToolCall(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newSubscriptionTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model: "gpt-5-codex",
		Messages: []core.Message{
			{Role: core.RoleUser, Content: "hi"},
			{
				Role:      core.RoleAssistant,
				Reasoning: []core.ReasoningItem{{ID: "rs_1", EncryptedContent: "enc-abc", Summary: "sum"}},
				ToolCalls: []core.ToolCall{{ID: "call_1", Name: "foo", Input: "{}"}},
			},
			{Role: core.RoleUser, ToolResult: &core.ToolResult{ToolCallID: "call_1", Content: "ok"}},
		},
	}))

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}

	reasoningIdx, funcIdx := -1, -1
	for i, item := range payload.Input {
		switch item["type"] {
		case "reasoning":
			reasoningIdx = i
			if item["encrypted_content"] != "enc-abc" {
				t.Errorf("reasoning encrypted_content = %v, want enc-abc", item["encrypted_content"])
			}
			if item["id"] != "rs_1" {
				t.Errorf("reasoning id = %v, want rs_1", item["id"])
			}
		case "function_call":
			funcIdx = i
		}
	}
	if reasoningIdx < 0 {
		t.Fatal("expected a reasoning input item echoed back")
	}
	if funcIdx < 0 {
		t.Fatal("expected a function_call input item")
	}
	if reasoningIdx > funcIdx {
		t.Errorf("reasoning item (idx %d) must precede its function_call (idx %d)", reasoningIdx, funcIdx)
	}
}

func TestSubscriptionCapturesReasoningFromResponse(t *testing.T) {
	transport := &captureStreamingTransport{stream: reasoningStreamBody}
	client := newSubscriptionTestClient(transport)

	chunks := drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:    "gpt-5-codex",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	}))

	var resp *llm.CompletionResponse
	for _, c := range chunks {
		if c.Type == llm.ChunkTypeDone {
			resp = c.Response
		}
	}
	if resp == nil {
		t.Fatal("no done chunk with a response")
	}
	if len(resp.Reasoning) != 1 {
		t.Fatalf("captured %d reasoning items, want 1: %+v", len(resp.Reasoning), resp.Reasoning)
	}
	got := resp.Reasoning[0]
	if got.ID != "rs_9" || got.EncryptedContent != "enc-xyz" || got.Summary != "thought" {
		t.Errorf("captured reasoning = %+v, want {rs_9, enc-xyz, thought}", got)
	}
}

func TestSubscriptionCatalogPropagatesAuthError(t *testing.T) {
	// A 401/403 from the catalog means the credential is bad or the account lacks
	// Codex access; ListModels must surface it (so connect fails) rather than
	// masking it with the static fallback.
	for _, status := range []int{401, 403} {
		transport := &captureStreamingTransport{
			status: status,
			stream: `{"error":{"message":"no access","type":"invalid_request_error"}}`,
		}
		client := newSubscriptionTestClient(transport)

		models, err := client.ListModels(context.Background())
		if err == nil {
			t.Errorf("status %d: expected an error, got fallback models %v", status, models)
		}
	}
}

func TestSubscriptionProviderRegistered(t *testing.T) {
	// Isolate the secret store to an empty HOME so there is no stored token.
	t.Setenv("HOME", t.TempDir())
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)

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

	p, err := llm.GetProvider(context.Background(), llm.OpenAI, llm.AuthSubscription)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.Name() != "openai:subscription" {
		t.Errorf("provider name = %q, want openai:subscription", p.Name())
	}

	// With no stored token, ListModels must surface the credential failure (from
	// the auth middleware, before any network call) rather than silently
	// returning the static fallback — otherwise connect, which verifies via
	// ListModels, would record a signed-out account as connected.
	if _, err := p.ListModels(context.Background()); err == nil {
		t.Error("ListModels without credentials should error, not return a static fallback")
	}
}

func TestSubscriptionCatalogFallsBackToStatic(t *testing.T) {
	// The fake transport returns an SSE body for the /models GET, which fails to
	// parse as a catalog, so ListModels falls back to the static list.
	client := newSubscriptionTestClient(&captureStreamingTransport{})

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != len(staticSubscriptionModels) {
		t.Fatalf("got %d models, want %d (static fallback)", len(models), len(staticSubscriptionModels))
	}

	idx := slices.IndexFunc(models, func(m llm.ModelInfo) bool { return m.ID == "gpt-5.4" })
	if idx < 0 {
		t.Fatalf("expected gpt-5.4 in the static fallback, got %v", models)
	}
	if models[idx].InputTokenLimit == 0 {
		t.Errorf("expected a non-zero context window for gpt-5.4")
	}
}

func TestSubscriptionCatalogParsesLiveResponse(t *testing.T) {
	transport := &captureStreamingTransport{stream: modelsCatalogBody}
	client := newSubscriptionTestClient(transport)

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	// The /models endpoint requires the client_version query param; without it
	// the backend 400s and we'd silently fall back.
	if !strings.Contains(transport.query, "client_version=") {
		t.Errorf("models request query = %q, want a client_version param", transport.query)
	}

	// Two visible entries; the show_in_picker=false one is dropped.
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2: %v", len(models), models)
	}
	byID := map[string]llm.ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if got, ok := byID["gpt-5.5"]; !ok || got.DisplayName != "GPT-5.5" || got.InputTokenLimit != 272000 {
		t.Errorf("gpt-5.5 = %+v (present=%v), want display GPT-5.5 / limit 272000", got, ok)
	}
	if _, hidden := byID["gpt-hidden"]; hidden {
		t.Error("show_in_picker=false model must be dropped")
	}
}
