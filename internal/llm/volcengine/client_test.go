package volcengine

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func TestName(t *testing.T) {
	c := NewClient(anthropicsdk.NewClient(), "volcengine:api_key")
	if got := c.Name(); got != "volcengine:api_key" {
		t.Fatalf("Name() = %q, want %q", got, "volcengine:api_key")
	}
}

func TestStaticModelsWithoutModel(t *testing.T) {
	t.Setenv("VOLCENGINE_MODEL", "")
	models := StaticModels()
	if len(models) != 0 {
		t.Fatalf("expected no models, got %d", len(models))
	}
}

func TestStaticModelsEnvOverride(t *testing.T) {
	t.Setenv("VOLCENGINE_MODEL", "ep-custom-model")
	models := StaticModels()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "ep-custom-model" {
		t.Fatalf("model ID = %q, want ep-custom-model", models[0].ID)
	}
}

func TestCatalogModel(t *testing.T) {
	t.Setenv("VOLCENGINE_MODEL", "ep-custom-model")
	info, ok := CatalogModel("ep-custom-model")
	if !ok {
		t.Fatal("expected model to be found in catalog")
	}
	if info.DisplayName != "ep-custom-model" {
		t.Fatalf("DisplayName = %q, want ep-custom-model", info.DisplayName)
	}

	_, ok = CatalogModel("unknown")
	if ok {
		t.Fatal("expected unknown model to not be found")
	}
}

func TestAPIKeyMeta(t *testing.T) {
	if APIKeyMeta.Provider != llm.Volcengine {
		t.Fatalf("provider = %q, want %q", APIKeyMeta.Provider, llm.Volcengine)
	}
	if APIKeyMeta.AuthMethod != llm.AuthAPIKey {
		t.Fatalf("auth method = %q, want %q", APIKeyMeta.AuthMethod, llm.AuthAPIKey)
	}
	if len(APIKeyMeta.EnvVars) != 1 || APIKeyMeta.EnvVars[0] != "VOLCENGINE_API_KEY" {
		t.Fatalf("env vars = %v, want [VOLCENGINE_API_KEY]", APIKeyMeta.EnvVars)
	}
	if APIKeyMeta.Key() != "volcengine:api_key" {
		t.Fatalf("key = %q, want volcengine:api_key", APIKeyMeta.Key())
	}
}

type modelsTransport struct {
	body string
	code int
	auth string
}

func (t *modelsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.auth = req.Header.Get("Authorization")
	code := t.code
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Status:     http.StatusText(code),
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

func TestListModelsFromAPI(t *testing.T) {
	transport := &modelsTransport{body: `{"data":[
		{"id":"ep-model-1","display_name":"Ark Model 1","task_type":["TextGeneration"],"modalities":{"output_modalities":["text"]},"token_limits":{"context_window":262144,"max_input_token_length":229376,"max_output_token_length":32768}},
		{"id":"ep-model-2","name":"Ark Model 2","task_type":["VisualQuestionAnswering"],"modalities":{"output_modalities":["text"]},"token_limits":{"context_window":32768,"max_output_token_length":12288}},
		{"id":"shutdown-model","name":"Shutdown","status":"Shutdown","task_type":["TextGeneration"],"modalities":{"output_modalities":["text"]}},
		{"id":"retiring-model","name":"Retiring","status":"Retiring","task_type":["TextGeneration"],"modalities":{"output_modalities":["text"]}},
		{"id":"embedding-model","name":"Embedding","task_type":["TextEmbedding"],"modalities":{}},
		{"id":"image-model","name":"Image","task_type":["TextToImage"],"modalities":{"output_modalities":["image"]}}
	]}`}
	withMockModelsClient(transport, func() {
		c := NewClientWithConfig(anthropicsdk.NewClient(), "volcengine:test", "https://example.com/api/coding", "test-token")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels() error = %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
		}
		if models[0].ID != "ep-model-1" || models[0].DisplayName != "Ark Model 1" {
			t.Fatalf("unexpected first model: %+v", models[0])
		}
		if models[0].InputTokenLimit != 229376 || models[0].OutputTokenLimit != 32768 {
			t.Fatalf("unexpected first model limits: %+v", models[0])
		}
		if models[1].InputTokenLimit != 32768 || models[1].OutputTokenLimit != 12288 {
			t.Fatalf("unexpected second model limits: %+v", models[1])
		}
		if transport.auth != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", transport.auth)
		}
	})
}

func TestListModelsFallbackToEnvModel(t *testing.T) {
	t.Setenv("VOLCENGINE_MODEL", "ep-fallback")
	withMockModelsClient(&modelsTransport{code: http.StatusUnauthorized}, func() {
		c := NewClientWithConfig(anthropicsdk.NewClient(), "volcengine:test", "https://example.com/api/coding", "test-token")
		models, err := c.ListModels(context.Background())
		if err != nil {
			t.Fatalf("ListModels() error = %v", err)
		}
		if len(models) != 1 || models[0].ID != "ep-fallback" {
			t.Fatalf("fallback models = %+v, want ep-fallback", models)
		}
	})
}

type captureTransport struct {
	auth string
	body []byte
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.auth = req.Header.Get("Authorization")
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		t.body = body
	}

	streamBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"doubao-seed-code","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

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

func TestStreamSendsBearerAuthAndRequest(t *testing.T) {
	transport := &captureTransport{}
	client := anthropicsdk.NewClient(
		anthropicoption.WithAuthToken("test-token"),
		anthropicoption.WithBaseURL("https://example.com/api/coding"),
		anthropicoption.WithHTTPClient(&http.Client{Transport: transport}),
	)
	c := NewClient(client, "volcengine:test")

	ch := c.Stream(context.Background(), llm.CompletionOptions{
		Model:    "doubao-seed-code",
		Messages: []core.Message{{Role: core.RoleUser, Content: "hi"}},
	})

	var text string
	var gotDone bool
	for chunk := range ch {
		switch chunk.Type {
		case llm.ChunkTypeText:
			text += chunk.Text
		case llm.ChunkTypeDone:
			gotDone = true
		case llm.ChunkTypeError:
			t.Fatalf("stream error: %v", chunk.Error)
		}
	}

	if transport.auth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", transport.auth)
	}
	if !strings.Contains(string(transport.body), `"model":"doubao-seed-code"`) {
		t.Fatalf("request body missing model: %s", string(transport.body))
	}
	if !gotDone {
		t.Fatal("stream ended without done chunk")
	}
	if text != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if defaultBaseURL != "https://ark.cn-beijing.volces.com/api/coding" {
		t.Fatalf("defaultBaseURL = %q", defaultBaseURL)
	}
}

func TestProviderImplementsInterface(t *testing.T) {
	var _ llm.Provider = (*Client)(nil)
}
