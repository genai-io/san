package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/llm"
)

type captureTransport struct {
	body []byte
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		t.body = b
	}

	streamBody := "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	return resp, nil
}

type modelsErrorTransport struct{}

func (t *modelsErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Status:     "503 Service Unavailable",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"ollama not running"}`)),
		Request:    req,
	}, nil
}

func TestOllamaListModelsPropagatesError(t *testing.T) {
	client := openai.NewClient(
		option.WithAPIKey("ollama"),
		option.WithBaseURL("http://localhost:11434/v1"),
		option.WithHTTPClient(&http.Client{Transport: &modelsErrorTransport{}}),
	)

	c := NewClient(client, "ollama:test")
	_, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error from models list request")
	}
}

func TestOllamaStreamSendsRequest(t *testing.T) {
	transport := &captureTransport{}
	client := openai.NewClient(
		option.WithAPIKey("ollama"),
		option.WithBaseURL("http://localhost:11434/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)

	c := NewClient(client, "ollama:test")

	messages := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
	}
	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:        "llama4",
		Messages:     messages,
		SystemPrompt: "sys",
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

	if payload["model"] != "llama4" {
		t.Fatalf("expected model llama4, got %v", payload["model"])
	}
}

func TestOllamaEstimateCost(t *testing.T) {
	cost, ok := EstimateCost("llama4", llm.Usage{
		InputTokens:  1000000,
		OutputTokens: 1000000,
	})
	if !ok {
		t.Fatal("expected pricing lookup to succeed")
	}
	// Ollama is local — cost should be zero.
	if cost.Amount != 0 {
		t.Fatalf("expected 0 cost for Ollama, got %.6f", cost.Amount)
	}
	if cost.Currency != llm.CurrencyUSD {
		t.Fatalf("expected USD, got %s", cost.Currency)
	}
}

func TestOllamaStaticModels(t *testing.T) {
	models := StaticModels()
	if len(models) == 0 {
		t.Fatal("expected non-empty static model catalog")
	}
	seen := map[string]bool{}
	for _, m := range models {
		if m.ID == "" {
			t.Fatal("model ID must not be empty")
		}
		if seen[m.ID] {
			t.Fatalf("duplicate model ID %q", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestOllamaCatalogModel(t *testing.T) {
	info, ok := CatalogModel("llama4")
	if !ok {
		t.Fatal("expected llama4 to be in catalog")
	}
	if info.DisplayName != "Llama 4" {
		t.Fatalf("expected DisplayName Llama 4, got %q", info.DisplayName)
	}

	_, ok = CatalogModel("nonexistent-model")
	if ok {
		t.Fatal("expected nonexistent-model to not be in catalog")
	}
}
