package llm

import (
	"context"
	"errors"
	"sync"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm/llmerr"
)

// defaultMaxTokens is the fallback max output tokens when neither the caller
// nor the provider specifies a limit.
const defaultMaxTokens = 8192

// completeMaxAttempts bounds in-place retries for one-shot utility completions.
const completeMaxAttempts = 3

// Client adapts a Provider to core.LLM.
//
// It also provides streaming and completion methods for the loop/app layer,
// plus cumulative token usage tracking.
//
// SetThinking can be called while the agent is running.
// Changes take effect on the next Infer/Stream call.
type Client struct {
	mu             sync.RWMutex
	provider       Provider
	model          string
	maxTokens      int
	inputLimit     int
	thinkingEffort string
	tokens         Usage

	// Token limits resolve from the provider's ListModels, which is a live
	// network round-trip for OpenAI-compatible providers (Anthropic/Google
	// cache it internally). A client's model is fixed for its lifetime, so the
	// resolved limits are memoized here to keep ListModels off the
	// per-inference-step hot path (InputLimit for compaction, output cap for
	// every Infer/Stream).
	inLimit  cachedLimit
	outLimit cachedLimit
}

// cachedLimit memoizes a token limit for the client's fixed model so a provider
// ListModels lookup runs once instead of on every inference step. Only a
// successful (non-zero) resolution is cached; a transient failure leaves the
// slot empty so the next call retries rather than sticking at 0 forever.
type cachedLimit struct {
	mu    sync.Mutex
	value int
}

// get returns the memoized limit, resolving it once via resolve(p, model). A
// result of 0 is treated as "unknown, retry later" and is not cached, so a
// transient provider failure retries rather than sticking at 0.
func (c *cachedLimit) get(p Provider, model string, resolve func(Provider, string) int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value == 0 {
		c.value = resolve(p, model)
	}
	return c.value
}

// NewClient wraps an existing provider as a core.LLM with streaming and
// completion support. maxTokens=0 means resolve from provider metadata
// or fall back to defaultMaxTokens.
func NewClient(p Provider, model string, maxTokens int) *Client {
	return &Client{provider: p, model: model, maxTokens: maxTokens}
}

// ---------------------------------------------------------------------------
// core.LLM interface
// ---------------------------------------------------------------------------

func (l *Client) Infer(ctx context.Context, req core.InferRequest) (<-chan core.Chunk, error) {
	l.mu.RLock()
	p := l.provider
	model := l.model
	thinking := l.thinkingEffort
	l.mu.RUnlock()

	opts := CompletionOptions{
		Model:          model,
		Messages:       toProviderMessages(req.Messages),
		Tools:          req.Tools,
		SystemPrompt:   req.System,
		MaxTokens:      l.effectiveMaxTokens(),
		ThinkingEffort: thinking,
	}

	srcCh := p.Stream(ctx, opts)

	ch := make(chan core.Chunk, 8)
	go func() {
		defer close(ch)
		// send forwards a chunk, aborting on ctx cancellation so this bridge
		// goroutine doesn't wedge when streamInfer exits via its ctx.Done.
		send := func(chunk core.Chunk) bool {
			select {
			case ch <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for sc := range srcCh {
			switch sc.Type {
			case ChunkTypeText:
				if !send(core.Chunk{Text: sc.Text}) {
					return
				}
			case ChunkTypeThinking:
				if !send(core.Chunk{Thinking: sc.Text}) {
					return
				}
			case ChunkTypeDone:
				if !send(core.Chunk{Done: true, Response: sc.Response}) {
					return
				}
			case ChunkTypeError:
				// Classify here so the agent loop can decide whether to
				// retry without importing the provider SDKs.
				send(core.Chunk{Err: llmerr.Wrap(sc.Error)})
				return
			}
		}
	}()

	return ch, nil
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// SetThinkingEffort changes the native thinking/reasoning effort value.
func (l *Client) SetThinkingEffort(effort string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.thinkingEffort = effort
}

// ThinkingEffort returns the current native thinking/reasoning effort value.
func (l *Client) ThinkingEffort() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.thinkingEffort
}

// ---------------------------------------------------------------------------
// Streaming & Completion (used by loop/app layer)
// ---------------------------------------------------------------------------

// Stream starts a streaming completion request and returns a chunk channel.
func (l *Client) Stream(ctx context.Context, msgs []core.Message,
	tools []ToolSchema, sysPrompt string,
) <-chan StreamChunk {
	return l.provider.Stream(ctx, l.completionOpts(msgs, tools, sysPrompt))
}

// Complete sends a one-shot completion (custom max tokens, no tools).
// Used for utility calls like conversation compaction.
func (l *Client) Complete(ctx context.Context,
	sysPrompt string, msgs []core.Message, maxTokens int,
) (CompletionResponse, error) {
	l.mu.RLock()
	model := l.model
	p := l.provider
	thinking := l.thinkingEffort
	l.mu.RUnlock()

	opts := CompletionOptions{
		Model:          model,
		SystemPrompt:   sysPrompt,
		Messages:       msgs,
		MaxTokens:      maxTokens,
		ThinkingEffort: thinking,
	}

	// Utility calls (e.g. compaction) are not streamed to the UI, so retry
	// them in place on transient failures, sharing the agent loop's backoff.
	var resp CompletionResponse
	var err error
	for attempt := 1; attempt <= completeMaxAttempts; attempt++ {
		if resp, err = Complete(ctx, p, opts); err == nil {
			return resp, nil
		}
		var re core.RetryableError
		if !errors.As(llmerr.Wrap(err), &re) || attempt == completeMaxAttempts {
			return resp, err
		}
		if werr := core.BackoffSleep(ctx, attempt, re.RetryAfter()); werr != nil {
			return resp, werr
		}
	}
	return resp, err
}

// send sends a non-streaming completion request and returns the full response.
func (l *Client) send(ctx context.Context, msgs []core.Message,
	tools []ToolSchema, sysPrompt string,
) (CompletionResponse, error) {
	return Complete(ctx, l.provider, l.completionOpts(msgs, tools, sysPrompt))
}

// ---------------------------------------------------------------------------
// Token Tracking
// ---------------------------------------------------------------------------

// AddUsage accumulates token usage from a completion response.
func (l *Client) AddUsage(usage Usage) {
	l.mu.Lock()
	l.tokens.InputTokens += usage.InputTokens
	l.tokens.OutputTokens += usage.OutputTokens
	l.tokens.CacheCreationInputTokens += usage.CacheCreationInputTokens
	l.tokens.CacheReadInputTokens += usage.CacheReadInputTokens
	l.mu.Unlock()
}

// Tokens returns the accumulated token usage.
func (l *Client) Tokens() Usage {
	l.mu.RLock()
	t := l.tokens
	l.mu.RUnlock()
	return t
}

// ---------------------------------------------------------------------------
// Identity & Limits
// ---------------------------------------------------------------------------

// Name returns the provider name (e.g., "anthropic").
func (l *Client) Name() string {
	l.mu.RLock()
	p := l.provider
	l.mu.RUnlock()
	if p == nil {
		return ""
	}
	return p.Name()
}

// ModelID returns the model identifier.
func (l *Client) ModelID() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.model
}

// SetInputLimit overrides the model's context window, mirroring how maxTokens
// overrides the output cap. The caller (the app) resolves the window the same
// way the status bar does — user setting, then the model store's cache — so
// the window the compaction check fires against is the one the user sees.
// Without it the two resolve independently and can disagree: a model an
// aggregator serves with no reported window leaves InputLimit at 0, disabling
// auto-compaction entirely while the bar still shows a healthy percentage.
func (l *Client) SetInputLimit(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputLimit = limit
}

// InputLimit returns the model's max input token capacity (context window),
// or 0 if unknown. An explicit SetInputLimit wins; otherwise the provider
// lookup is memoized per model, so this is cheap to call on every inference
// step (the compaction check does so).
func (l *Client) InputLimit() int {
	l.mu.RLock()
	p := l.provider
	model := l.model
	override := l.inputLimit
	l.mu.RUnlock()
	if override > 0 {
		return override
	}
	return l.inLimit.get(p, model, inputLimitFromProvider)
}

// ResolveMaxTokens returns the effective output token limit.
// Priority: 1. Custom override (maxTokens field)
//
//  2. Provider's model metadata (OutputTokenLimit from ListModels)
//  3. Default (8192)
func (l *Client) ResolveMaxTokens(context.Context) int {
	return l.effectiveMaxTokens()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// effectiveMaxTokens resolves the output-token cap: an explicit maxTokens
// override wins, otherwise the memoized provider limit, otherwise the default.
func (l *Client) effectiveMaxTokens() int {
	l.mu.RLock()
	p := l.provider
	model := l.model
	mt := l.maxTokens
	l.mu.RUnlock()

	if mt > 0 {
		return mt
	}
	if limit := l.outLimit.get(p, model, outputLimitFromProvider); limit > 0 {
		return limit
	}
	return defaultMaxTokens
}

// completionOpts builds CompletionOptions from the Client's current configuration.
func (l *Client) completionOpts(msgs []core.Message, tools []ToolSchema, sysPrompt string) CompletionOptions {
	l.mu.RLock()
	model := l.model
	thinking := l.thinkingEffort
	l.mu.RUnlock()

	return CompletionOptions{
		Model:          model,
		Messages:       msgs,
		MaxTokens:      l.effectiveMaxTokens(),
		Tools:          tools,
		SystemPrompt:   sysPrompt,
		ThinkingEffort: thinking,
	}
}

// inputLimitFromProvider queries the provider for the model's input token limit.
func inputLimitFromProvider(p Provider, model string) int {
	if p == nil {
		return 0
	}
	models, err := p.ListModels(context.TODO())
	if err == nil {
		for _, m := range models {
			if m.ID == model && m.InputTokenLimit > 0 {
				return m.InputTokenLimit
			}
		}
	}
	if fetcher, ok := p.(ModelLimitsFetcher); ok {
		input, _, err := fetcher.FetchModelLimits(context.TODO(), model)
		if err == nil {
			return input
		}
	}
	return 0
}

// outputLimitFromProvider queries the provider for the model's output token limit.
func outputLimitFromProvider(p Provider, model string) int {
	if p == nil {
		return 0
	}
	models, err := p.ListModels(context.TODO())
	if err == nil {
		for _, m := range models {
			if m.ID == model && m.OutputTokenLimit > 0 {
				return m.OutputTokenLimit
			}
		}
	}
	if fetcher, ok := p.(ModelLimitsFetcher); ok {
		_, output, err := fetcher.FetchModelLimits(context.TODO(), model)
		if err == nil {
			return output
		}
	}
	return 0
}

// toProviderMessages converts core messages for provider consumption, keeping
// only the fields a provider needs. A tool result is a RoleUser message with a
// non-nil ToolResult; user-typed text is a RoleUser message without one.
func toProviderMessages(msgs []core.Message) []core.Message {
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case core.RoleUser:
			if m.ToolResult != nil {
				out = append(out, core.Message{
					Role:       core.RoleUser,
					ToolResult: m.ToolResult,
				})
			} else {
				out = append(out, core.Message{
					Role:    core.RoleUser,
					Content: m.Content,
					Images:  m.Images,
				})
			}
		case core.RoleAssistant:
			out = append(out, core.Message{
				Role:              core.RoleAssistant,
				Content:           m.Content,
				Thinking:          m.Thinking,
				ThinkingSignature: m.ThinkingSignature,
				Reasoning:         m.Reasoning,
				ToolCalls:         m.ToolCalls,
			})
		}
	}
	return out
}
