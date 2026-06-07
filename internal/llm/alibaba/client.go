// Package alibaba implements the Provider interface using the Alibaba Cloud DashScope platform.
// DashScope's API is OpenAI-compatible, so we reuse the openai-go SDK with a custom base URL.
package alibaba

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openaicompat"
)

// Client implements the Provider interface for Qwen using the OpenAI SDK.
type Client struct {
	client openai.Client
	name   string
}

// NewClient creates a new Qwen client with the given OpenAI SDK client.
func NewClient(client openai.Client, name string) *Client {
	return &Client{client: client, name: name}
}

// Name returns the provider name.
func (c *Client) Name() string { return c.name }

// isThinkingModel returns true if the model supports thinking/reasoning mode.
func isThinkingModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "qwq") || strings.Contains(lower, "thinking")
}

// makeAssistantConverter returns a provider-specific assistant message converter.
// For thinking models, reasoning_content is injected only when present.
func makeAssistantConverter(thinking bool) func(core.Message) openai.ChatCompletionMessageParamUnion {
	if !thinking {
		return openaicompat.DefaultAssistantMessage
	}
	return func(msg core.Message) openai.ChatCompletionMessageParamUnion {
		return openaicompat.AssistantMessageWithReasoning(msg, msg.Thinking)
	}
}

func thinkingBudget(effort string) int {
	switch effort {
	case "think":
		return 5000
	case "think+":
		return 32000
	case "ultrathink":
		return 128000
	default:
		return 0
	}
}

func (c *Client) ThinkingEfforts(model string) []string {
	if !isThinkingModel(model) {
		return nil
	}
	return []string{"off", "think", "think+", "ultrathink"}
}

func (c *Client) DefaultThinkingEffort(model string) string {
	if !isThinkingModel(model) {
		return ""
	}
	return "off"
}

// Stream sends a completion request and returns a channel of streaming chunks.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	thinking := isThinkingModel(opts.Model)
	return openaicompat.StreamChatCompletions(ctx, openaicompat.ChatStreamConfig{
		Client:           c.client,
		ProviderName:     c.name,
		Options:          opts,
		ConvertAssistant: makeAssistantConverter(thinking),
		ConfigureParams: func(params *openai.ChatCompletionNewParams) {
			if !thinking {
				return
			}
			extraFields := map[string]any{"enable_thinking": true}
			if budget := thinkingBudget(opts.ThinkingEffort); budget > 0 {
				extraFields["thinking_budget"] = budget
			}
			params.SetExtraFields(extraFields)
		},
		ExtractReasoning: thinking,
	})
}

// ListModels returns the available models for Qwen by querying the DashScope API.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))
	for _, m := range page.Data {
		models = append(models, llm.ModelInfo{ID: m.ID, Name: m.ID, DisplayName: m.ID})
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return models, nil
}

// FetchModelLimits queries the DashScope model detail API to get token limits.
// DashScope returns extra_info.default_envs with max_input_tokens and max_output_tokens.
func (c *Client) FetchModelLimits(ctx context.Context, modelID string) (inputLimit, outputLimit int, err error) {
	model, err := c.client.Models.Get(ctx, modelID)
	if err != nil {
		return 0, 0, err
	}

	raw := model.RawJSON()
	if raw == "" {
		return 0, 0, nil
	}

	var detail struct {
		ExtraInfo struct {
			DefaultEnvs struct {
				MaxInputTokens  int `json:"max_input_tokens"`
				MaxOutputTokens int `json:"max_output_tokens"`
			} `json:"default_envs"`
		} `json:"extra_info"`
	}
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return 0, 0, nil
	}

	return detail.ExtraInfo.DefaultEnvs.MaxInputTokens, detail.ExtraInfo.DefaultEnvs.MaxOutputTokens, nil
}

// Ensure Client implements Provider and ModelLimitsFetcher
var _ llm.Provider = (*Client)(nil)
var _ llm.ModelLimitsFetcher = (*Client)(nil)
