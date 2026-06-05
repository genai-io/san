package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
)

// Name represents a provider name
type Name string

const (
	Anthropic Name = "anthropic"
	OpenAI    Name = "openai"
	Google    Name = "google"
	Moonshot  Name = "moonshot"
	Alibaba   Name = "alibaba"
	MinMax    Name = "minmax"
	BigModel  Name = "bigmodel"
	DeepSeek  Name = "deepseek"
	SenseNova Name = "sensenova"
	Ollama    Name = "ollama"
)

// AuthMethod represents an authentication method for an LLM provider.
type AuthMethod string

const (
	AuthAPIKey  AuthMethod = "api_key"
	AuthVertex  AuthMethod = "vertex"
	AuthBedrock AuthMethod = "bedrock"
)

// Meta contains static metadata about a provider
type Meta struct {
	Provider    Name
	AuthMethod  AuthMethod
	EnvVars     []string // Required environment variables
	DisplayName string
}

// Key returns a unique key for this provider configuration
func (m Meta) Key() string {
	return string(m.Provider) + ":" + string(m.AuthMethod)
}

// ThinkingEffortProvider is implemented by providers that expose native thinking
// or reasoning effort values.
type ThinkingEffortProvider interface {
	ThinkingEfforts(model string) []string
	DefaultThinkingEffort(model string) string
}

func ThinkingEfforts(p Provider, model string) []string {
	ep, ok := p.(ThinkingEffortProvider)
	if !ok {
		return nil
	}
	efforts := ep.ThinkingEfforts(model)
	if len(efforts) == 0 {
		return nil
	}
	out := make([]string, len(efforts))
	copy(out, efforts)
	return out
}

func DefaultThinkingEffort(p Provider, model string) string {
	ep, ok := p.(ThinkingEffortProvider)
	if !ok {
		return ""
	}
	return normalizeThinkingEffort(ep.DefaultThinkingEffort(model), ep.ThinkingEfforts(model))
}

func ResolveThinkingEffort(p Provider, model, selected string) string {
	efforts := ThinkingEfforts(p, model)
	if len(efforts) == 0 {
		return ""
	}
	if effort := normalizeThinkingEffort(selected, efforts); effort != "" {
		return effort
	}
	return DefaultThinkingEffort(p, model)
}

func NextThinkingEffort(p Provider, model, current string) (string, bool) {
	efforts := ThinkingEfforts(p, model)
	if len(efforts) == 0 {
		return "", false
	}
	current = normalizeThinkingEffort(current, efforts)
	if current == "" {
		current = DefaultThinkingEffort(p, model)
	}
	for i, effort := range efforts {
		if effort == current {
			return efforts[(i+1)%len(efforts)], true
		}
	}
	return efforts[0], true
}

func normalizeThinkingEffort(effort string, efforts []string) string {
	effort = strings.TrimSpace(strings.ToLower(effort))
	if effort == "" {
		return ""
	}
	for _, allowed := range efforts {
		if strings.EqualFold(effort, allowed) {
			return allowed
		}
	}
	return ""
}

// ModelInfo represents information about an available model
type ModelInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	DisplayName      string `json:"displayName,omitempty"`
	InputTokenLimit  int    `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit int    `json:"outputTokenLimit,omitempty"`
}

// CompletionOptions contains options for a completion request
type CompletionOptions struct {
	Model          string
	Messages       []core.Message
	MaxTokens      int
	Temperature    float64
	Tools          []ToolSchema
	SystemPrompt   string
	ThinkingEffort string
}

// --- Completion Response Types ---

// CompletionResponse represents a completion response from an LLM provider.
type CompletionResponse struct {
	Content           string          `json:"content,omitempty"`
	Thinking          string          `json:"thinking,omitempty"`
	ThinkingSignature string          `json:"thinking_signature,omitempty"`
	ToolCalls         []core.ToolCall `json:"tool_calls,omitempty"`
	StopReason        string          `json:"stop_reason"`
	Usage             Usage           `json:"usage"`
}

// Logging accessors — satisfy duck-typed interfaces in the log package so
// log does not need to import llm (foundation-layer contract).
func (r CompletionResponse) LogStopReason() string { return r.StopReason }
func (r CompletionResponse) LogContent() string    { return r.Content }
func (r CompletionResponse) LogThinking() string   { return r.Thinking }
func (r CompletionResponse) LogInputTokens() int   { return r.Usage.InputTokens }
func (r CompletionResponse) LogOutputTokens() int  { return r.Usage.OutputTokens }
func (r CompletionResponse) LogRawToolCalls() any  { return r.ToolCalls }
func (r CompletionResponse) LogRawUsage() any      { return r.Usage }

func (r CompletionResponse) LogToolCallSummary(escaper func(string) string) string {
	if len(r.ToolCalls) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "    ToolCalls(%d):\n", len(r.ToolCalls))
	for _, tc := range r.ToolCalls {
		fmt.Fprintf(&sb, "      [%s] %s(%s)\n", tc.ID, tc.Name, escaper(tc.Input))
	}
	return sb.String()
}

// Usage contains token usage information.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// --- Streaming Types ---

// ChunkType represents the type of a stream chunk from a provider.
type ChunkType string

const (
	ChunkTypeText      ChunkType = "text"
	ChunkTypeThinking  ChunkType = "thinking"
	ChunkTypeToolStart ChunkType = "tool_start"
	ChunkTypeToolInput ChunkType = "tool_input"
	ChunkTypeDone      ChunkType = "done"
	ChunkTypeError     ChunkType = "error"
)

// StreamChunk represents a chunk in a streaming response from a provider.
type StreamChunk struct {
	Type     ChunkType
	Text     string              // For text chunks
	ToolID   string              // For tool_start chunks
	ToolName string              // For tool_start chunks
	Response *CompletionResponse // For done chunks
	Error    error               // For error chunks
}

// ToolSchema is a backward-compatible alias for core.ToolSchema.
type ToolSchema = core.ToolSchema

// Provider is the interface that all providers must implement
type Provider interface {
	// Stream sends a completion request and returns a channel of streaming chunks
	Stream(ctx context.Context, opts CompletionOptions) <-chan StreamChunk

	// ListModels returns the available models for this provider
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// Name returns the provider name
	Name() string
}

// ModelLimitsFetcher is an optional interface for providers that can fetch
// token limits for a specific model via API (e.g. DashScope model detail endpoint).
type ModelLimitsFetcher interface {
	FetchModelLimits(ctx context.Context, modelID string) (inputLimit, outputLimit int, err error)
}

// Factory creates a new Provider instance
type Factory func(ctx context.Context) (Provider, error)

// Complete is a helper function that collects stream chunks into a complete response
// This provides non-streaming output from any Provider
func Complete(ctx context.Context, provider Provider, opts CompletionOptions) (CompletionResponse, error) {
	var response CompletionResponse

	streamChan := provider.Stream(ctx, opts)

	gotDone := false
	for chunk := range streamChan {
		switch chunk.Type {
		case ChunkTypeText:
			response.Content += chunk.Text
		case ChunkTypeToolStart, ChunkTypeToolInput:
			// Tool calls are accumulated in the done chunk
		case ChunkTypeDone:
			if chunk.Response != nil {
				return *chunk.Response, nil
			}
			gotDone = true
		case ChunkTypeError:
			return response, chunk.Error
		}
	}

	if !gotDone {
		return response, fmt.Errorf("stream closed without completion")
	}
	return response, nil
}
