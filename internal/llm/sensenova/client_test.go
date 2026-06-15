package sensenova

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

// --- Unit tests ---

func TestName(t *testing.T) {
	c := NewClient(openai.NewClient(), "sensenova:api_key")
	if got := c.Name(); got != "sensenova:api_key" {
		t.Fatalf("Name() = %q, want %q", got, "sensenova:api_key")
	}
}

func TestListModelsReturnsStaticCatalog(t *testing.T) {
	c := NewClient(openai.NewClient(), "sensenova:api_key")
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "sensenova-6.7-flash-lite" {
		t.Fatalf("expected sensenova-6.7-flash-lite, got %q", models[0].ID)
	}
	if models[0].InputTokenLimit != 256000 {
		t.Fatalf("expected input limit 256000, got %d", models[0].InputTokenLimit)
	}
	if models[0].OutputTokenLimit != 65536 {
		t.Fatalf("expected output limit 65536, got %d", models[0].OutputTokenLimit)
	}
	if models[1].ID != "deepseek-v4-flash" {
		t.Fatalf("expected deepseek-v4-flash, got %q", models[1].ID)
	}
	if models[1].InputTokenLimit != 1_000_000 {
		t.Fatalf("expected input limit 1000000, got %d", models[1].InputTokenLimit)
	}
	if models[1].OutputTokenLimit != 384000 {
		t.Fatalf("expected output limit 384000, got %d", models[1].OutputTokenLimit)
	}
}

func TestCatalogModel(t *testing.T) {
	info, ok := CatalogModel("sensenova-6.7-flash-lite")
	if !ok {
		t.Fatal("expected model to be found in catalog")
	}
	if info.DisplayName != "SenseNova 6.7 Flash Lite" {
		t.Fatalf("expected SenseNova 6.7 Flash Lite, got %q", info.DisplayName)
	}

	_, ok = CatalogModel("nonexistent-model")
	if ok {
		t.Fatal("expected nonexistent model to not be found")
	}
}

func TestStaticModels(t *testing.T) {
	models := StaticModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 static models, got %d", len(models))
	}
	if models[0].ID != "sensenova-6.7-flash-lite" {
		t.Fatalf("expected sensenova-6.7-flash-lite, got %q", models[0].ID)
	}
	if models[1].ID != "deepseek-v4-flash" {
		t.Fatalf("expected deepseek-v4-flash, got %q", models[1].ID)
	}
}

func TestAPIKeyMeta(t *testing.T) {
	if APIKeyMeta.Provider != llm.SenseNova {
		t.Fatalf("expected provider sensenova, got %q", APIKeyMeta.Provider)
	}
	if APIKeyMeta.AuthMethod != llm.AuthAPIKey {
		t.Fatalf("expected auth method api_key, got %q", APIKeyMeta.AuthMethod)
	}
	if len(APIKeyMeta.EnvVars) != 1 || APIKeyMeta.EnvVars[0] != "SENSENOVA_API_KEY" {
		t.Fatalf("expected env var SENSENOVA_API_KEY, got %v", APIKeyMeta.EnvVars)
	}
	if APIKeyMeta.Key() != "sensenova:api_key" {
		t.Fatalf("expected key sensenova:api_key, got %q", APIKeyMeta.Key())
	}
	if APIKeyMeta.DisplayName != "Bearer Token API" {
		t.Fatalf("expected display name 'Bearer Token API', got %q", APIKeyMeta.DisplayName)
	}
}

func TestProviderImplementsInterface(t *testing.T) {
	var _ llm.Provider = (*Client)(nil)
}

// --- Integration tests with mocked OpenAI-compatible server ---

type sseTransport struct {
	body   []byte
	auth   string
	stream string
}

func (t *sseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.auth = req.Header.Get("Authorization")
	if req.Body != nil {
		t.body, _ = io.ReadAll(req.Body)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(t.stream)),
	}
	return resp, nil
}

// OpenAI-style SSE: deltas followed by a final chunk with usage and [DONE].
const openAIStreamFixture = `data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello!"},"finish_reason":null}]}

data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}

data: [DONE]

`

func TestStreamIntegration(t *testing.T) {
	transport := &sseTransport{stream: openAIStreamFixture}
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	c := NewClient(client, "sensenova:api_key")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "sensenova-6.7-flash-lite",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})

	var text string
	var gotDone bool
	var gotError error
	var inputTokens, outputTokens int
	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkTypeText:
			text += chunk.Text
		case llm.ChunkTypeDone:
			gotDone = true
			if chunk.Response != nil {
				inputTokens = chunk.Response.Usage.InputTokens
				outputTokens = chunk.Response.Usage.OutputTokens
			}
		case llm.ChunkTypeError:
			gotError = chunk.Error
		}
	}

	if gotError != nil {
		t.Fatalf("stream error: %v", gotError)
	}
	if !gotDone {
		t.Fatal("stream ended without done chunk")
	}
	if text != "Hello!" {
		t.Fatalf("text = %q, want %q", text, "Hello!")
	}
	if inputTokens != 12 {
		t.Errorf("input_tokens = %d, want 12", inputTokens)
	}
	if outputTokens != 3 {
		t.Errorf("output_tokens = %d, want 3", outputTokens)
	}
}

func TestStreamSendsBearerAuth(t *testing.T) {
	transport := &sseTransport{stream: "data: [DONE]\n\n"}
	client := openai.NewClient(
		option.WithAPIKey("test-bearer-key"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	c := NewClient(client, "sensenova:api_key")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "sensenova-6.7-flash-lite",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})
	for range ch {
	}

	if transport.auth != "Bearer test-bearer-key" {
		t.Fatalf("Authorization header = %q, want %q", transport.auth, "Bearer test-bearer-key")
	}
}

func TestStreamSendsIncludeUsage(t *testing.T) {
	transport := &sseTransport{stream: "data: [DONE]\n\n"}
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	c := NewClient(client, "sensenova:api_key")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "sensenova-6.7-flash-lite",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})
	for range ch {
	}

	if len(transport.body) == 0 {
		t.Fatal("no request body captured")
	}
	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}
	streamOpts, ok := payload["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options missing from request body")
	}
	if streamOpts["include_usage"] != true {
		t.Errorf("stream_options.include_usage = %v, want true", streamOpts["include_usage"])
	}
}
