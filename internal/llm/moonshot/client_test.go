package moonshot

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

type modelsTransport struct{ body string }

func (t *modelsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

func TestMoonshotAssistantMessagesIncludeReasoningContent(t *testing.T) {
	transport := &captureTransport{}
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)

	c := NewClient(client, "moonshot:test")

	messages := []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "tc1", Name: "WebSearch", Input: "{}"}}},
		{Role: core.RoleUser, ToolResult: &core.ToolResult{ToolCallID: "tc1", Content: "ok"}},
		{Role: core.RoleAssistant, Content: "done"},
	}

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:        "kimi-k2.5",
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

	rawMsgs, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("messages not found in payload")
	}

	for i, raw := range rawMsgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		if _, ok := msg["reasoning_content"]; !ok {
			t.Fatalf("assistant message missing reasoning_content at index %d", i)
		}
	}
}

func TestMoonshotListModelsReturnsErrorOnAPIFailure(t *testing.T) {
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: &modelsErrorTransport{}}),
	)

	c := NewClient(client, "moonshot:test")

	models, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected ListModels to fail")
	}
	if len(models) != 0 {
		t.Fatalf("expected no fallback models, got %d", len(models))
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestMoonshotListModelsUsesContextLengthFromAPI(t *testing.T) {
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: &modelsTransport{body: `{
			"object": "list",
			"data": [
				{"id": "kimi-k2.6", "object": "model", "context_length": 262144},
				{"id": "kimi-k3", "object": "model", "context_length": 1048576}
			]
		}`}}),
	)
	c := NewClient(client, "moonshot:test")

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	byID := make(map[string]llm.ModelInfo, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	if got := byID["kimi-k2.6"].InputTokenLimit; got != 262_144 {
		t.Errorf("kimi-k2.6 context limit = %d, want 262144", got)
	}
	if got := byID["kimi-k3"].InputTokenLimit; got != 1_048_576 {
		t.Errorf("kimi-k3 context limit = %d, want 1048576", got)
	}
}

func TestMoonshotListModelsFallsBackWhenContextLengthMissing(t *testing.T) {
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: &modelsTransport{body: `{
			"object": "list",
			"data": [{"id": "kimi-k2.6", "object": "model"}]
		}`}}),
	)
	c := NewClient(client, "moonshot:test")

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	if got := models[0].InputTokenLimit; got != 262_144 {
		t.Errorf("fallback context limit = %d, want 262144", got)
	}
}
