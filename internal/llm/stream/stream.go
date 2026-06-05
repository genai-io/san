package stream

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/llm"
	"github.com/genai-io/san/v2/internal/log"
)

// State tracks common streaming response state across provider implementations.
type State struct {
	ProviderName string
	Start        time.Time
	ChunkCount   int
	Response     llm.CompletionResponse

	contentBuf  strings.Builder
	thinkingBuf strings.Builder
}

// NewState creates a new stream state for a provider.
func NewState(providerName string) *State {
	return &State{
		ProviderName: providerName,
		Start:        time.Now(),
	}
}

// Count records one more upstream stream event/chunk.
func (s *State) Count() {
	s.ChunkCount++
}

// send forwards chunk to ch, aborting on ctx cancellation so a goroutine
// holding the stream doesn't wedge forever when the consumer (streamInfer)
// has bailed out via its own ctx.Done branch.
func send(ctx context.Context, ch chan<- llm.StreamChunk, chunk llm.StreamChunk) bool {
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// EmitText forwards a text delta and accumulates it into the response.
func (s *State) EmitText(ctx context.Context, ch chan<- llm.StreamChunk, text string) {
	if text == "" {
		return
	}
	if !send(ctx, ch, llm.StreamChunk{Type: llm.ChunkTypeText, Text: text}) {
		return
	}
	s.contentBuf.WriteString(text)
}

// EmitThinking forwards a thinking delta and accumulates it into the response.
func (s *State) EmitThinking(ctx context.Context, ch chan<- llm.StreamChunk, text string) {
	if text == "" {
		return
	}
	if !send(ctx, ch, llm.StreamChunk{Type: llm.ChunkTypeThinking, Text: text}) {
		return
	}
	s.thinkingBuf.WriteString(text)
}

// EmitToolStart forwards a tool start event.
func (s *State) EmitToolStart(ctx context.Context, ch chan<- llm.StreamChunk, toolID, toolName string) {
	send(ctx, ch, llm.StreamChunk{
		Type:     llm.ChunkTypeToolStart,
		ToolID:   toolID,
		ToolName: toolName,
	})
}

// EmitToolInput forwards a tool input delta.
func (s *State) EmitToolInput(ctx context.Context, ch chan<- llm.StreamChunk, toolID, text string) {
	if text == "" {
		return
	}
	send(ctx, ch, llm.StreamChunk{
		Type:   llm.ChunkTypeToolInput,
		ToolID: toolID,
		Text:   text,
	})
}

// UpdateUsage updates the tracked usage values when the provider emits them.
func (s *State) UpdateUsage(inputTokens, outputTokens int) {
	if inputTokens > 0 {
		s.Response.Usage.InputTokens = inputTokens
	}
	if outputTokens > 0 {
		s.Response.Usage.OutputTokens = outputTokens
	}
}

// UpdateCacheUsage records prompt-caching token counts from the provider response.
func (s *State) UpdateCacheUsage(cacheCreation, cacheRead int) {
	if cacheCreation > 0 {
		s.Response.Usage.CacheCreationInputTokens = cacheCreation
	}
	if cacheRead > 0 {
		s.Response.Usage.CacheReadInputTokens = cacheRead
	}
}

// AddToolCallsSorted appends tool calls from an indexed accumulator in stable index order.
func (s *State) AddToolCallsSorted(toolCalls map[int]*core.ToolCall) {
	for _, idx := range slices.Sorted(maps.Keys(toolCalls)) {
		s.Response.ToolCalls = append(s.Response.ToolCalls, *toolCalls[idx])
	}
}

// AddToolCallsByKey appends tool calls from a string-keyed accumulator in stable key order.
func (s *State) AddToolCallsByKey(toolCalls map[string]*core.ToolCall) {
	for _, key := range slices.Sorted(maps.Keys(toolCalls)) {
		s.Response.ToolCalls = append(s.Response.ToolCalls, *toolCalls[key])
	}
}

// EnsureToolUseStopReason infers tool_use when tool calls exist but no stop reason was set.
func (s *State) EnsureToolUseStopReason() {
	if len(s.Response.ToolCalls) > 0 && s.Response.StopReason == "" {
		s.Response.StopReason = "tool_use"
	}
}

// Fail logs and emits a terminal error chunk.
func (s *State) Fail(ctx context.Context, ch chan<- llm.StreamChunk, err error) {
	log.LogError(s.ProviderName, err)
	send(ctx, ch, llm.StreamChunk{
		Type:  llm.ChunkTypeError,
		Error: err,
	})
}

// Finish logs stream completion, logs the final response, and emits the done chunk.
// It copies the response so the receiver does not retain a pointer into State,
// allowing the State (and its string builders) to be GC'd.
//
// The Done chunk is sent via the ctx-aware send helper so a cancel that races
// the provider's natural stream completion doesn't wedge this goroutine on an
// unbuffered channel after the bridge has already exited via ctx.Done.
func (s *State) Finish(ctx context.Context, ch chan<- llm.StreamChunk) {
	s.Response.Content = s.contentBuf.String()
	s.Response.Thinking = s.thinkingBuf.String()
	log.LogStreamDone(s.ProviderName, time.Since(s.Start), s.ChunkCount)
	log.LogResponseCtx(ctx, s.ProviderName, s.Response)
	resp := s.Response // shallow copy — breaks the pointer into State
	send(ctx, ch, llm.StreamChunk{
		Type:     llm.ChunkTypeDone,
		Response: &resp,
	})
}
