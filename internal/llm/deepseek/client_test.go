package deepseek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
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
		Body:       io.NopCloser(io.Reader(strings.NewReader(streamBody))),
	}
	return resp, nil
}

type modelsErrorTransport struct{}

func (t *modelsErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Invalid Authentication","type":"invalid_authentication_error"}`)),
		Request:    req,
	}, nil
}

func TestDeepSeekListModelsFallsBackToStatic(t *testing.T) {
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: &modelsErrorTransport{}}),
	)

	c := NewClient(client, "deepseek:test")
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("expected static fallback models")
	}
	if models[0].ID != "deepseek-chat" {
		t.Fatalf("expected deepseek-chat, got %s", models[0].ID)
	}
}

func TestDeepSeekStreamSendsRequest(t *testing.T) {
	transport := &captureTransport{}
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)

	c := NewClient(client, "deepseek:test")

	messages := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
	}
	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:        "deepseek-chat",
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

	if payload["model"] != "deepseek-chat" {
		t.Fatalf("expected model deepseek-chat, got %v", payload["model"])
	}
}

func TestDeepSeekEstimateCost(t *testing.T) {
	cost, ok := EstimateCost("deepseek-chat", llm.Usage{
		InputTokens:  1000000,
		OutputTokens: 1000000,
	})
	if !ok {
		t.Fatal("expected pricing lookup to succeed")
	}
	if cost.Amount != 1.37 {
		t.Fatalf("expected 1.37, got %.6f", cost.Amount)
	}
	if cost.Currency != llm.CurrencyUSD {
		t.Fatalf("expected USD, got %s", cost.Currency)
	}
}

func TestDeepSeekIsReasonerModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"deepseek-chat", false},
		{"deepseek-reasoner", true},
	}
	for _, tt := range tests {
		got := isReasonerModel(tt.model)
		if got != tt.expected {
			t.Errorf("isReasonerModel(%q) = %v, want %v", tt.model, got, tt.expected)
		}
	}
}
