package core

import "context"

// StopReason describes why the LLM stopped generating.
type StopReason string

const (
	StopEndTurn                    StopReason = "end_turn"
	StopMaxTokens                  StopReason = "max_tokens"
	StopToolUse                    StopReason = "tool_use"
	StopMaxSteps                   StopReason = "max_steps"
	StopCancelled                  StopReason = "cancelled"
	StopHook                       StopReason = "stop_hook"
	StopMaxOutputRecoveryExhausted StopReason = "max_output_recovery_exhausted"
)

// InferRequest is sent to the LLM for inference.
type InferRequest struct {
	System   string       // assembled system prompt
	Messages []Message    // conversation history
	Tools    []ToolSchema // available tools
}

// Usage is the token accounting for one LLM call. Field names use the project's
// domain vocabulary; the json tags preserve each provider's wire format (e.g.
// Anthropic's cache_creation_input_tokens). It lives in core, the foundation
// layer, so both core.InferResponse and the llm provider response share one
// definition (llm.Usage aliases this).
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// InferResponse is the final aggregated response from one LLM call.
type InferResponse struct {
	Content           string     // text output
	Thinking          string     // chain-of-thought (extended thinking)
	ThinkingSignature string     // signature for replaying thinking blocks
	ToolCalls         []ToolCall // tool execution requests
	StopReason        StopReason
	Usage
}

// TotalInputTokens is the full prompt size the model processed: fresh input
// plus the cached portion (e.g. Anthropic reports the cache-marked system
// prompt under CacheRead/CacheCreation, not InputTokens). This is the figure
// that reflects context-window occupancy; InputTokens alone undercounts it
// whenever prompt caching is active.
func (r InferResponse) TotalInputTokens() int {
	return r.InputTokens + r.CacheCreationInputTokens + r.CacheReadInputTokens
}

// Chunk is one piece of a streaming LLM response.
type Chunk struct {
	Text     string // incremental text
	Thinking string // incremental thinking
	Done     bool   // true on final chunk

	Response *InferResponse // non-nil only when Done=true
	Err      error          // non-nil on stream error
}

// LLM is the inference abstraction — call a language model.
//
// Infer sends a request and returns a channel of streaming chunks.
// The channel is closed when the response is complete or on error.
// The final chunk has Done=true and carries the aggregated InferResponse.
//
// InputLimit returns the model's max input token capacity (context window).
// Returns 0 if unknown — auto compaction is disabled in that case.
type LLM interface {
	Infer(ctx context.Context, req InferRequest) (<-chan Chunk, error)
	InputLimit() int
}
