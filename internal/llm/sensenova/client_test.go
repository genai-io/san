package sensenova

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/llm"
)

// --- Unit tests ---

func TestName(t *testing.T) {
	c := NewClient(anthropicsdk.NewClient(), "sensenova:api_key")
	if got := c.Name(); got != "sensenova:api_key" {
		t.Fatalf("Name() = %q, want %q", got, "sensenova:api_key")
	}
}

func TestListModelsReturnsStaticCatalog(t *testing.T) {
	c := NewClient(anthropicsdk.NewClient(), "sensenova:api_key")
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "sensenova-6.7-flash-lite" {
		t.Fatalf("expected sensenova-6.7-flash-lite, got %q", models[0].ID)
	}
	if models[0].InputTokenLimit != 128000 {
		t.Fatalf("expected input limit 128000, got %d", models[0].InputTokenLimit)
	}
	if models[0].OutputTokenLimit != 65536 {
		t.Fatalf("expected output limit 65536, got %d", models[0].OutputTokenLimit)
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
	if len(models) != 1 {
		t.Fatalf("expected 1 static model, got %d", len(models))
	}
	if models[0].ID != "sensenova-6.7-flash-lite" {
		t.Fatalf("expected sensenova-6.7-flash-lite, got %q", models[0].ID)
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

// --- Integration tests with mocked SenseNova server ---

// senseNovaServer returns an httptest.Server that mimics the SenseNova
// Anthropic-compatible Messages API.
func senseNovaServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			handleMessagesEndpoint(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func handleMessagesEndpoint(w http.ResponseWriter, r *http.Request) {
	// Verify Bearer auth
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Forbidden"}})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	// Send a minimal Anthropic-compatible SSE streaming response
	events := []string{
		`event: message_start
data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"sensenova-6.7-flash-lite","usage":{"input_tokens":10,"output_tokens":0}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}`,
		`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`event: message_stop
data: {"type":"message_stop"}`,
	}
	for _, event := range events {
		fmt.Fprintf(w, "%s\n\n", event)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestStreamIntegration(t *testing.T) {
	server := senseNovaServer(t)
	defer server.Close()

	client := anthropicsdk.NewClient(
		anthropicoption.WithAuthToken("test-key"),
		anthropicoption.WithBaseURL(server.URL),
	)
	c := NewClient(client, "sensenova:api_key")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "sensenova-6.7-flash-lite",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})

	var text string
	var gotDone bool
	var gotError error
	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkTypeText:
			text += chunk.Text
		case llm.ChunkTypeDone:
			gotDone = true
			if chunk.Response != nil {
				if chunk.Response.StopReason != "end_turn" {
					t.Errorf("stop_reason = %q, want end_turn", chunk.Response.StopReason)
				}
				if chunk.Response.Usage.OutputTokens != 5 {
					t.Errorf("output_tokens = %d, want 5", chunk.Response.Usage.OutputTokens)
				}
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
}

func TestStreamSendsBearerAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := anthropicsdk.NewClient(
		anthropicoption.WithAuthToken("test-bearer-key"),
		anthropicoption.WithBaseURL(server.URL),
	)
	c := NewClient(client, "sensenova:api_key")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "sensenova-6.7-flash-lite",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})
	for range ch {
	}

	if receivedAuth != "Bearer test-bearer-key" {
		t.Fatalf("Authorization header = %q, want %q", receivedAuth, "Bearer test-bearer-key")
	}
}
