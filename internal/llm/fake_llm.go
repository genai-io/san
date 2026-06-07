package llm

import (
	"context"
	"sync"

	"github.com/genai-io/san/internal/core"
)

// FakeLLM is a test double that returns predefined responses.
// It supports both streaming and non-streaming modes, tool calls,
// and multiple sequential responses for multi-turn conversations.
//
// Usage:
//
//	fake := &llm.FakeLLM{
//	    Responses: []CompletionResponse{
//	        {Content: "hello", StopReason: "end_turn"},
//	    },
//	}
//	// Use fake.Send() or fake.Stream() — both draw from the same Responses slice.
type FakeLLM struct {
	// Responses is the queue of responses to return, consumed in order.
	// Each call to Send/Stream pops the first entry. If exhausted,
	// a default "no more responses" reply is returned.
	Responses []CompletionResponse

	// Model name (defaults to "fake-model")
	Model string

	// ProviderName (defaults to "fake")
	ProviderName string

	// Calls records every set of CompletionOptions received, in order.
	Calls []CompletionOptions

	// ErrorAt injects an error on the Nth call (1-based). 0 means disabled.
	ErrorAt int

	// ErrorValue is the error to inject when ErrorAt triggers.
	ErrorValue error

	// mu protects mutable state (callCount, Responses, Calls).
	mu sync.Mutex

	// callCount tracks total calls across Send/Stream/Complete.
	callCount int
}

// Send returns the next response synchronously.
func (f *FakeLLM) Send(_ context.Context, msgs []core.Message,
	tools []ToolSchema, sysPrompt string,
) (CompletionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordCallLocked(msgs, tools, sysPrompt)
	if f.shouldInjectErrorLocked() {
		return CompletionResponse{}, f.ErrorValue
	}
	return f.nextLocked(), nil
}

// Stream returns the next response as a single-chunk stream.
func (f *FakeLLM) Stream(_ context.Context, msgs []core.Message,
	tools []ToolSchema, sysPrompt string,
) <-chan StreamChunk {
	f.mu.Lock()
	f.recordCallLocked(msgs, tools, sysPrompt)
	ch := make(chan StreamChunk, 1)

	var chunk StreamChunk
	if f.shouldInjectErrorLocked() {
		chunk = StreamChunk{Type: ChunkTypeError, Error: f.ErrorValue}
	} else {
		resp := f.nextLocked()
		chunk = StreamChunk{Type: ChunkTypeDone, Response: &resp}
	}
	f.mu.Unlock()

	go func() {
		ch <- chunk
		close(ch)
	}()
	return ch
}

// Complete returns the next response (used for utility calls like compaction).
func (f *FakeLLM) Complete(_ context.Context,
	sysPrompt string, msgs []core.Message, maxTokens int,
) (CompletionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, CompletionOptions{
		Model:        f.modelID(),
		SystemPrompt: sysPrompt,
		Messages:     msgs,
		MaxTokens:    maxTokens,
	})
	if f.shouldInjectErrorLocked() {
		return CompletionResponse{}, f.ErrorValue
	}
	return f.nextLocked(), nil
}

// Name returns the provider name.
func (f *FakeLLM) Name() string {
	if f.ProviderName != "" {
		return f.ProviderName
	}
	return "fake"
}

// ModelID returns the model identifier.
func (f *FakeLLM) ModelID() string {
	return f.modelID()
}

// ResolveMaxTokens returns a fixed default for testing.
func (f *FakeLLM) ResolveMaxTokens(_ context.Context) int {
	return defaultMaxTokens
}

// --- helpers (must be called with f.mu held) ---

func (f *FakeLLM) shouldInjectErrorLocked() bool {
	f.callCount++
	return f.ErrorAt > 0 && f.callCount == f.ErrorAt
}

func (f *FakeLLM) nextLocked() CompletionResponse {
	if len(f.Responses) == 0 {
		return CompletionResponse{
			Content:    "no more responses",
			StopReason: "end_turn",
		}
	}
	resp := f.Responses[0]
	f.Responses = f.Responses[1:]
	return resp
}

func (f *FakeLLM) modelID() string {
	if f.Model != "" {
		return f.Model
	}
	return "fake-model"
}

func (f *FakeLLM) recordCallLocked(msgs []core.Message, tools []ToolSchema, sysPrompt string) {
	f.Calls = append(f.Calls, CompletionOptions{
		Model:        f.modelID(),
		Messages:     msgs,
		Tools:        tools,
		SystemPrompt: sysPrompt,
	})
}
