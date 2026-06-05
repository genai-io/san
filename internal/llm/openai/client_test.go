package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

type captureStreamingTransport struct {
	body []byte
	path string
}

func (t *captureStreamingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		t.body = body
	}
	t.path = req.URL.Path

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(responsesStreamBody)),
		Request:    req,
	}, nil
}

const responsesStreamBody = "" +
	"data: {\"type\":\"response.reasoning_summary_text.delta\",\"item_id\":\"rs_1\",\"output_index\":0,\"summary_index\":0,\"delta\":\"thinking...\"}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":1,\"content_index\":0,\"delta\":\"ok\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2,\"output_tokens_details\":{\"reasoning_tokens\":1}}}}\n\n" +
	"data: [DONE]\n\n"

func newTestClient(t *captureStreamingTransport) *Client {
	client := sdk.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://example.com/v1"),
		option.WithHTTPClient(&http.Client{Transport: t}),
	)
	return NewClient(client, "openai:test")
}

func drain(ch <-chan llm.StreamChunk) []llm.StreamChunk {
	var chunks []llm.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestOpenAIThinkingEfforts(t *testing.T) {
	client := newTestClient(&captureStreamingTransport{})
	got := client.ThinkingEfforts("gpt-5.5")
	want := []string{"none", "low", "medium", "high", "xhigh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ThinkingEfforts() = %#v, want %#v", got, want)
	}
	if client.DefaultThinkingEffort("gpt-5.5") != "medium" {
		t.Fatalf("expected default effort medium")
	}

	got = client.ThinkingEfforts("gpt-5")
	want = []string{"high"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ThinkingEfforts(gpt-5) = %#v, want %#v", got, want)
	}
	if client.DefaultThinkingEffort("gpt-5") != "high" {
		t.Fatalf("expected default effort high")
	}

	if got := client.ThinkingEfforts("gpt-4.1"); len(got) != 0 {
		t.Fatalf("ThinkingEfforts(gpt-4.1) = %#v, want nil", got)
	}
	if got := client.DefaultThinkingEffort("gpt-4.1"); got != "" {
		t.Fatalf("DefaultThinkingEffort(gpt-4.1) = %q, want empty", got)
	}
}

func TestStreamUsesResponsesAPIForGpt54(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:          "gpt-5.4",
		Messages:       []core.Message{{Role: core.RoleUser, Content: "hi"}},
		ThinkingEffort: "none",
	}))

	if transport.path != "/v1/responses" {
		t.Fatalf("expected responses path, got %q", transport.path)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}

	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in payload")
	}
	if got, _ := reasoning["effort"].(string); got != "none" {
		t.Fatalf("expected reasoning.effort=none, got %#v", reasoning["effort"])
	}
}

func TestStreamResponsesIncludesReasoningSummaryAndEmitsThinking(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newTestClient(transport)

	chunks := drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model:          "gpt-5.4",
		Messages:       []core.Message{{Role: core.RoleUser, Content: "hi"}},
		ThinkingEffort: "xhigh",
	}))

	if transport.path != "/v1/responses" {
		t.Fatalf("expected responses path, got %q", transport.path)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}

	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in payload")
	}
	if got, _ := reasoning["effort"].(string); got != "xhigh" {
		t.Fatalf("expected reasoning.effort=xhigh, got %#v", reasoning["effort"])
	}
	if got, _ := reasoning["summary"].(string); got != "auto" {
		t.Fatalf("expected reasoning.summary=auto, got %#v", reasoning["summary"])
	}

	foundThinking := false
	for _, chunk := range chunks {
		if chunk.Type == llm.ChunkTypeThinking && chunk.Text == "thinking..." {
			foundThinking = true
			break
		}
	}
	if !foundThinking {
		t.Fatal("expected reasoning summary delta to emit a thinking chunk")
	}
}

func TestStreamResponsesIncludesImageInputs(t *testing.T) {
	transport := &captureStreamingTransport{}
	client := newTestClient(transport)

	drain(client.Stream(context.Background(), llm.CompletionOptions{
		Model: "gpt-5.4",
		Messages: []core.Message{{
			Role:    core.RoleUser,
			Content: "describe this",
			Images: []core.Image{{
				MediaType: "image/png",
				Data:      "ZmFrZQ==",
			}},
		}},
	}))

	var payload map[string]any
	if err := json.Unmarshal(transport.body, &payload); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}

	input, ok := payload["input"].([]any)
	if !ok || len(input) == 0 {
		t.Fatalf("expected input items in payload")
	}
	message, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first input item to be a message")
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("expected text+image content array, got %#v", message["content"])
	}

	var imagePart map[string]any
	for _, part := range content {
		item, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := item["type"].(string); got == "input_image" {
			imagePart = item
			break
		}
	}
	if imagePart == nil {
		t.Fatalf("expected at least one input_image content part, got %#v", content)
	}
	if got, _ := imagePart["image_url"].(string); got != "data:image/png;base64,ZmFrZQ==" {
		t.Fatalf("expected data URL image, got %#v", imagePart["image_url"])
	}
}
