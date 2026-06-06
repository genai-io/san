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

// InferResponse is the final aggregated response from one LLM call.
type InferResponse struct {
	Content                  string     // text output
	Thinking                 string     // chain-of-thought (extended thinking)
	ThinkingSignature        string     // signature for replaying thinking blocks
	ToolCalls                []ToolCall // tool execution requests
	StopReason               StopReason
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
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
