package openaicompat

import (
	"context"

	"github.com/openai/openai-go/v3"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	streamutil "github.com/genai-io/san/internal/llm/stream"
	"github.com/genai-io/san/internal/log"
)

// ChatStreamConfig contains provider-specific knobs for OpenAI-compatible
// Chat Completions streaming.
type ChatStreamConfig struct {
	Client           openai.Client
	ProviderName     string
	Options          llm.CompletionOptions
	ConvertAssistant func(core.Message) openai.ChatCompletionMessageParamUnion
	ConfigureParams  func(*openai.ChatCompletionNewParams)
	ExtractReasoning bool
}

// StreamChatCompletions streams an OpenAI-compatible Chat Completions request.
func StreamChatCompletions(ctx context.Context, cfg ChatStreamConfig) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk)

	go func() {
		defer close(ch)

		opts := cfg.Options
		messages := ConvertMessages(opts.Messages, opts.SystemPrompt, cfg.ConvertAssistant)

		params := openai.ChatCompletionNewParams{
			Model:    opts.Model,
			Messages: messages,
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
		}
		if opts.MaxTokens > 0 {
			params.MaxCompletionTokens = openai.Int(int64(opts.MaxTokens))
		}
		if opts.Temperature > 0 {
			params.Temperature = openai.Float(opts.Temperature)
		}
		if len(opts.Tools) > 0 {
			params.Tools = ConvertTools(opts.Tools)
		}
		if cfg.ConfigureParams != nil {
			cfg.ConfigureParams(&params)
		}

		log.LogRequestCtx(ctx, cfg.ProviderName, opts.Model, opts)

		stream := cfg.Client.Chat.Completions.NewStreaming(ctx, params)
		state := streamutil.NewState(cfg.ProviderName)
		toolCalls := make(map[int]*core.ToolCall)

		for stream.Next() {
			chunk := stream.Current()
			state.Count()

			for _, choice := range chunk.Choices {
				if cfg.ExtractReasoning {
					if content := ExtractReasoningContent(choice.Delta.RawJSON()); content != "" {
						state.EmitThinking(ctx, ch, content)
					}
				}
				if choice.Delta.Content != "" {
					state.EmitText(ctx, ch, choice.Delta.Content)
				}

				for _, tc := range choice.Delta.ToolCalls {
					idx := int(tc.Index)
					if _, exists := toolCalls[idx]; !exists {
						toolCalls[idx] = &core.ToolCall{ID: tc.ID, Name: tc.Function.Name}
						state.EmitToolStart(ctx, ch, tc.ID, tc.Function.Name)
					}
					if tc.Function.Arguments != "" {
						toolCalls[idx].Input += tc.Function.Arguments
						state.EmitToolInput(ctx, ch, toolCalls[idx].ID, tc.Function.Arguments)
					}
				}

				if choice.FinishReason != "" {
					state.Response.StopReason = MapFinishReason(choice.FinishReason)
				}
			}

			// OpenAI-compatible APIs report prompt_tokens as the FULL prompt
			// (cached tokens included) and expose the cached slice under
			// prompt_tokens_details.cached_tokens. Split it so InputTokens holds
			// only the fresh tokens and the cached portion lands in
			// CacheReadInputTokens — the Anthropic convention the rest of the app
			// assumes. Without the split, summing InputTokens across a turn's
			// infer steps multi-counts the re-read cache (inflating the ↑ usage
			// readout) and cost bills the cached prefix at the full input rate
			// instead of the cache-read rate. TotalInputTokens stays the full
			// prompt, so the ctx occupancy readout is unchanged.
			cachedTokens := int(chunk.Usage.PromptTokensDetails.CachedTokens)
			state.UpdateUsage(int(chunk.Usage.PromptTokens)-cachedTokens, int(chunk.Usage.CompletionTokens))
			state.UpdateCacheUsage(0, cachedTokens)
		}

		if err := stream.Err(); err != nil {
			state.Fail(ctx, ch, NormalizeAPIError(cfg.ProviderName, err))
			return
		}

		state.AddToolCallsSorted(toolCalls)
		state.EnsureToolUseStopReason()
		state.Finish(ctx, ch)
	}()

	return ch
}
