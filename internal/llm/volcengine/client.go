package volcengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/genai-io/san/internal/llm"
	anthropicprovider "github.com/genai-io/san/internal/llm/anthropic"
)

var modelsClient = &http.Client{Timeout: 10 * time.Second}

type Client struct {
	inner   *anthropicprovider.Client
	baseURL string
	apiKey  string
}

func NewClient(client anthropicsdk.Client, name string) *Client {
	return &Client{inner: anthropicprovider.NewClient(client, name)}
}

func NewClientWithConfig(client anthropicsdk.Client, name, baseURL, apiKey string) *Client {
	return &Client{
		inner:   anthropicprovider.NewClient(client, name),
		baseURL: baseURL,
		apiKey:  apiKey,
	}
}

func (c *Client) Name() string { return c.inner.Name() }

func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return c.inner.Stream(ctx, opts)
}

type modelEntry struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	TaskType    []string `json:"task_type"`
	Modalities  struct {
		Output []string `json:"output_modalities"`
	} `json:"modalities"`
	TokenLimits struct {
		ContextWindow int `json:"context_window"`
		MaxInput      int `json:"max_input_token_length"`
		MaxOutput     int `json:"max_output_token_length"`
	} `json:"token_limits"`
}

type modelsResponse struct {
	Data []modelEntry `json:"data"`
}

func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	models, err := c.fetchModels(ctx)
	if err == nil && len(models) > 0 {
		return models, nil
	}
	return StaticModels(), nil
}

func (c *Client) fetchModels(ctx context.Context) ([]llm.ModelInfo, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, fmt.Errorf("missing volcengine model API configuration")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := modelsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list volcengine models: %s", resp.Status)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if !isUsableModel(m) {
			continue
		}
		models = append(models, modelInfo(m))
	}
	return models, nil
}

func isUsableModel(m modelEntry) bool {
	if strings.EqualFold(m.Status, "Shutdown") || strings.EqualFold(m.Status, "Retiring") {
		return false
	}
	if !containsFold(m.Modalities.Output, "text") && !containsFold(m.TaskType, "TextGeneration") && !containsFold(m.TaskType, "VisualQuestionAnswering") {
		return false
	}
	return m.ID != ""
}

func modelInfo(m modelEntry) llm.ModelInfo {
	name := m.DisplayName
	if name == "" {
		name = m.Name
	}
	if name == "" {
		name = m.ID
	}
	inputLimit := m.TokenLimits.MaxInput
	if inputLimit == 0 {
		inputLimit = m.TokenLimits.ContextWindow
	}
	return llm.ModelInfo{
		ID:               m.ID,
		Name:             name,
		DisplayName:      name,
		InputTokenLimit:  inputLimit,
		OutputTokenLimit: m.TokenLimits.MaxOutput,
	}
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

var _ llm.Provider = (*Client)(nil)
