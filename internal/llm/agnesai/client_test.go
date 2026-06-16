package agnesai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type modelsTransport struct {
	body string
}

func (t *modelsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
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

func newTestClient(transport http.RoundTripper) *Client {
	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://apihub.agnes-ai.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	return NewClient(client, "agnesai:test")
}

func TestAgnesAIListModelsReturnsAPIResults(t *testing.T) {
	transport := &modelsTransport{
		body: `{
			"object": "list",
			"data": [
				{"id": "agnes-2.0-flash", "object": "model", "context_length": 262144},
				{"id": "agnes-1.5-flash", "object": "model", "context_length": 131072}
			]
		}`,
	}
	c := newTestClient(transport)

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "agnes-1.5-flash" {
		t.Fatalf("expected first model agnes-1.5-flash (sorted), got %s", models[0].ID)
	}
	if models[1].ID != "agnes-2.0-flash" {
		t.Fatalf("expected second model agnes-2.0-flash, got %s", models[1].ID)
	}
	if models[1].InputTokenLimit != 262144 {
		t.Fatalf("expected agnes-2.0-flash input limit 262144, got %d", models[1].InputTokenLimit)
	}
}

func TestAgnesAIListModelsFallsBackToStaticLimit(t *testing.T) {
	// If the API omits context_length, the static fallback in catalog.go
	// should kick in so the status bar can still render.
	transport := &modelsTransport{
		body: `{
			"object": "list",
			"data": [
				{"id": "agnes-1.5-flash", "object": "model"},
				{"id": "agnes-2.0-flash", "object": "model"}
			]
		}`,
	}
	c := newTestClient(transport)

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	byID := map[string]int{}
	for _, m := range models {
		byID[m.ID] = m.InputTokenLimit
	}
	if byID["agnes-1.5-flash"] != 128_000 {
		t.Errorf("agnes-1.5-flash limit = %d, want 128000 (static fallback)", byID["agnes-1.5-flash"])
	}
	if byID["agnes-2.0-flash"] != 256_000 {
		t.Errorf("agnes-2.0-flash limit = %d, want 256000 (static fallback)", byID["agnes-2.0-flash"])
	}
}

func TestAgnesAIListModelsReturnsErrorOnAPIFailure(t *testing.T) {
	c := newTestClient(&modelsErrorTransport{})

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

func TestStaticInputLimit(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"agnes-2.0-flash", 256_000},
		{"agnes-1.5-flash", 128_000},
		{"AGNES-2.0-FLASH", 256_000}, // case-insensitive
		{"unknown-model", 0},
	}
	for _, c := range cases {
		if got := staticInputLimit(c.model); got != c.want {
			t.Errorf("staticInputLimit(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}
