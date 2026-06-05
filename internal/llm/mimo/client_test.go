package mimo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/llm"
)

// modelsTransport returns a mock models API response.
type modelsTransport struct {
	body string
	code int
}

func (t *modelsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	code := t.code
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

func withMockModelsClient(transport http.RoundTripper, fn func()) {
	orig := modelsClient
	modelsClient = &http.Client{Transport: transport}
	defer func() { modelsClient = orig }()
	fn()
}

func TestListModels_FromAPI(t *testing.T) {
	body := `{"data":[
		{"id":"xiaomi/mimo-v2.5-pro","name":"MiMo V2.5 Pro","context_length":1048576,"max_output_length":131072},
		{"id":"xiaomi/mimo-v2-flash","name":"MiMo V2 Flash","context_length":262144,"max_output_length":65536}
	]}`
	withMockModelsClient(&modelsTransport{body: body}, func() {
		c := NewClient(anthropicsdk.NewClient(), "mimo:test")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("expected 2 models, got %d", len(models))
		}
		if models[0].ID != "xiaomi/mimo-v2.5-pro" {
			t.Errorf("expected first model xiaomi/mimo-v2.5-pro, got %s", models[0].ID)
		}
		if models[0].InputTokenLimit != 1048576 {
			t.Errorf("expected input limit 1048576, got %d", models[0].InputTokenLimit)
		}
	})
}

func TestListModels_FallbackOnError(t *testing.T) {
	withMockModelsClient(&modelsTransport{code: http.StatusUnauthorized}, func() {
		c := NewClient(anthropicsdk.NewClient(), "mimo:test")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) == 0 {
			t.Fatal("expected static fallback models, got 0")
		}
		found := false
		for _, m := range models {
			if m.ID == "xiaomi/mimo-v2.5-pro" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected xiaomi/mimo-v2.5-pro in fallback models")
		}
	})
}

func TestListModels_FallbackOnNetworkError(t *testing.T) {
	withMockModelsClient(&modelsTransport{code: http.StatusInternalServerError}, func() {
		c := NewClient(anthropicsdk.NewClient(), "mimo:test")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != len(catalog) {
			t.Fatalf("expected %d fallback models, got %d", len(catalog), len(models))
		}
	})
}

func TestListModels_CatalogLookup(t *testing.T) {
	body := `{"data":[{"id":"xiaomi/mimo-v2.5-pro","name":"API Name","context_length":999,"max_output_length":999}]}`
	withMockModelsClient(&modelsTransport{body: body}, func() {
		c := NewClient(anthropicsdk.NewClient(), "mimo:test")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != 1 {
			t.Fatalf("expected 1 model, got %d", len(models))
		}
		if models[0].InputTokenLimit != 1048576 {
			t.Errorf("expected catalog input limit 1048576, got %d", models[0].InputTokenLimit)
		}
		if models[0].DisplayName != "MiMo V2.5 Pro" {
			t.Errorf("expected catalog display name 'MiMo V2.5 Pro', got %s", models[0].DisplayName)
		}
	})
}

func TestEstimateCost(t *testing.T) {
	cost, ok := EstimateCost("xiaomi/mimo-v2.5-pro", llm.Usage{
		InputTokens:  1000000,
		OutputTokens: 1000000,
	})
	if !ok {
		t.Fatal("expected pricing lookup to succeed")
	}
	if cost.Amount < 1.304 || cost.Amount > 1.306 {
		t.Fatalf("expected ~1.305, got %.6f", cost.Amount)
	}
	if cost.Currency != llm.CurrencyUSD {
		t.Fatalf("expected USD, got %s", cost.Currency)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	_, ok := EstimateCost("unknown-model", llm.Usage{InputTokens: 1000, OutputTokens: 1000})
	if ok {
		t.Fatal("expected false for unknown model")
	}
}

func TestStaticModels(t *testing.T) {
	models := StaticModels()
	if len(models) != len(catalog) {
		t.Fatalf("expected %d static models, got %d", len(catalog), len(models))
	}
}

func TestCatalogModel(t *testing.T) {
	info, ok := CatalogModel("xiaomi/mimo-v2.5-pro")
	if !ok {
		t.Fatal("expected to find xiaomi/mimo-v2.5-pro in catalog")
	}
	if info.InputTokenLimit != 1048576 {
		t.Errorf("expected 1048576, got %d", info.InputTokenLimit)
	}
}

func TestCatalogModel_NotFound(t *testing.T) {
	_, ok := CatalogModel("nonexistent")
	if ok {
		t.Fatal("expected false for nonexistent model")
	}
}

// captureStreamTransport captures the request body and returns a minimal SSE stream.
type captureStreamTransport struct {
	body []byte
}

func (t *captureStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		t.body = b
	}

	streamBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"xiaomi/mimo-v2.5-pro","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
		Request:    req,
	}, nil
}

func TestStreamSendsRequest(t *testing.T) {
	transport := &captureStreamTransport{}
	client := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey("test"),
		anthropicoption.WithBaseURL("https://example.com"),
		anthropicoption.WithHTTPClient(&http.Client{Transport: transport}),
	)

	c := NewClient(client, "mimo:test")
	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "xiaomi/mimo-v2.5-pro",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})
	for range ch {
	}

	if len(transport.body) == 0 {
		t.Fatal("no request body captured")
	}
	if !strings.Contains(string(transport.body), "xiaomi/mimo-v2.5-pro") {
		t.Errorf("expected model in request body, got: %s", string(transport.body))
	}
}

func TestStreamReceivesText(t *testing.T) {
	transport := &captureStreamTransport{}
	client := anthropicsdk.NewClient(
		anthropicoption.WithAPIKey("test"),
		anthropicoption.WithBaseURL("https://example.com"),
		anthropicoption.WithHTTPClient(&http.Client{Transport: transport}),
	)

	c := NewClient(client, "mimo:test")
	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "xiaomi/mimo-v2.5-pro",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})

	var textChunks []string
	var done *llm.CompletionResponse
	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkTypeText:
			textChunks = append(textChunks, chunk.Text)
		case llm.ChunkTypeDone:
			done = chunk.Response
		}
	}

	if len(textChunks) == 0 {
		t.Fatal("expected text chunks")
	}
	if textChunks[0] != "hello" {
		t.Errorf("expected 'hello', got %q", textChunks[0])
	}
	if done == nil {
		t.Fatal("expected done chunk")
	}
	if done.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", done.StopReason)
	}
}
