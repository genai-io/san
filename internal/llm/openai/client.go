package openai

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/openaicompat"
	streamutil "github.com/genai-io/san/internal/llm/stream"
	"github.com/genai-io/san/internal/log"
)

// Client implements the Provider interface using the OpenAI SDK
type Client struct {
	client     openai.Client
	name       string
	limitMu    sync.Mutex
	limitCache map[string]modelTokenLimits
}

// NewClient creates a new OpenAI client with the given SDK client
func NewClient(client openai.Client, name string) *Client {
	return &Client{
		client:     client,
		name:       name,
		limitCache: make(map[string]modelTokenLimits),
	}
}

// Name returns the provider name
func (c *Client) Name() string {
	return c.name
}

// Stream sends a completion request and returns a channel of streaming chunks.
// OpenAI is implemented via the Responses API only.
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	return c.streamResponses(ctx, opts)
}

// streamResponses implements streaming via the Responses API.
func (c *Client) streamResponses(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk)

	go func() {
		defer close(ch)

		// Convert messages to Responses API input items
		var inputItems responses.ResponseInputParam = make([]responses.ResponseInputItemUnionParam, 0, len(opts.Messages)+1)

		for _, msg := range openaicompat.DropEmptyMessages(openaicompat.SanitizeToolMessages(opts.Messages)) {
			if msg.ToolResult != nil {
				inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: msg.ToolResult.ToolCallID,
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.Opt(msg.ToolResult.Content),
						},
					},
				})
				continue
			}
			switch msg.Role {
			case core.RoleUser:
				inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
					OfMessage: responseMessageParam(responses.EasyInputMessageRoleUser, msg),
				})
			case core.RoleAssistant:
				if len(msg.ToolCalls) > 0 {
					// Add text content as a message if present
					if messageHasResponseContent(msg) {
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfMessage: responseMessageParam(responses.EasyInputMessageRoleAssistant, msg),
						})
					}
					// Add each tool call as a separate function_call input item
					for _, tc := range msg.ToolCalls {
						inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
							OfFunctionCall: &responses.ResponseFunctionToolCallParam{
								CallID:    tc.ID,
								Name:      tc.Name,
								Arguments: tc.Input,
							},
						})
					}
				} else {
					if !messageHasResponseContent(msg) {
						continue
					}
					inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
						OfMessage: responseMessageParam(responses.EasyInputMessageRoleAssistant, msg),
					})
				}
			default: // system messages
				if !messageHasResponseContent(msg) {
					continue
				}
				inputItems = append(inputItems, responses.ResponseInputItemUnionParam{
					OfMessage: responseMessageParam(responses.EasyInputMessageRoleSystem, msg),
				})
			}
		}

		// Build request params
		params := responses.ResponseNewParams{
			Model: opts.Model,
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: inputItems,
			},
		}

		if opts.SystemPrompt != "" {
			params.Instructions = openai.Opt(opts.SystemPrompt)
		}

		if opts.MaxTokens > 0 {
			params.MaxOutputTokens = openai.Opt(int64(opts.MaxTokens))
		}

		if opts.Temperature > 0 {
			params.Temperature = openai.Opt(opts.Temperature)
		}

		if opts.ThinkingEffort != "" {
			params.Reasoning = shared.ReasoningParam{
				Effort:  shared.ReasoningEffort(opts.ThinkingEffort),
				Summary: shared.ReasoningSummaryAuto,
			}
		}

		// Add tools if provided
		if len(opts.Tools) > 0 {
			tools := make([]responses.ToolUnionParam, len(opts.Tools))
			for i, t := range opts.Tools {
				var funcParams map[string]any
				if props, ok := t.Parameters.(map[string]any); ok {
					funcParams = props
				}
				tools[i] = responses.ToolUnionParam{
					OfFunction: &responses.FunctionToolParam{
						Name:        t.Name,
						Description: openai.Opt(t.Description),
						Parameters:  funcParams,
					},
				}
			}
			params.Tools = tools
		}

		// Log request
		log.LogRequestCtx(ctx, c.name, opts.Model, opts)

		// Create streaming request
		stream := c.client.Responses.NewStreaming(ctx, params)

		state := streamutil.NewState(c.name)

		// Track tool calls by item ID
		toolCalls := make(map[string]*core.ToolCall)
		hasToolCalls := false

		// Read stream events
		for stream.Next() {
			event := stream.Current()
			state.Count()

			switch event.Type {
			case "response.output_text.delta":
				delta := event.AsResponseOutputTextDelta()
				state.EmitText(ctx, ch, delta.Delta)

			case "response.reasoning_summary_text.delta":
				delta := event.AsResponseReasoningSummaryTextDelta()
				state.EmitThinking(ctx, ch, delta.Delta)

			case "response.reasoning_text.delta":
				delta := event.AsResponseReasoningTextDelta()
				state.EmitThinking(ctx, ch, delta.Delta)

			case "response.output_item.added":
				itemEvent := event.AsResponseOutputItemAdded()
				if itemEvent.Item.Type == "function_call" {
					funcCall := itemEvent.Item.AsFunctionCall()
					hasToolCalls = true
					toolCalls[funcCall.ID] = &core.ToolCall{
						ID:   funcCall.CallID,
						Name: funcCall.Name,
					}
					state.EmitToolStart(ctx, ch, funcCall.CallID, funcCall.Name)
				}

			case "response.function_call_arguments.delta":
				delta := event.AsResponseFunctionCallArgumentsDelta()
				if tc, ok := toolCalls[delta.ItemID]; ok {
					tc.Input += delta.Delta
					state.EmitToolInput(ctx, ch, tc.ID, delta.Delta)
				}

			case "response.completed":
				completed := event.AsResponseCompleted()
				resp := completed.Response

				// Map usage
				state.UpdateUsage(int(resp.Usage.InputTokens), int(resp.Usage.OutputTokens))

				// Determine stop reason
				switch resp.Status {
				case responses.ResponseStatusCompleted:
					if hasToolCalls {
						state.Response.StopReason = "tool_use"
					} else {
						state.Response.StopReason = "end_turn"
					}
				case responses.ResponseStatusIncomplete:
					state.Response.StopReason = "max_tokens"
				case responses.ResponseStatusFailed:
					errMsg := "response failed"
					if resp.Error.Message != "" {
						errMsg = resp.Error.Message
					}
					state.Fail(ctx, ch, fmt.Errorf("responses API failed: %s", errMsg))
					return
				default:
					state.Response.StopReason = string(resp.Status)
				}

			case "error":
				errEvent := event.AsError()
				state.Fail(ctx, ch, fmt.Errorf("responses API error: %s", errEvent.Message))
				return
			}
		}

		if err := stream.Err(); err != nil {
			state.Fail(ctx, ch, openaicompat.NormalizeAPIError(c.name, err))
			return
		}

		state.AddToolCallsByKey(toolCalls)
		state.EnsureToolUseStopReason()
		state.Finish(ctx, ch)
	}()

	return ch
}

// ListModels returns the available models for OpenAI using the API
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	// Use OpenAI API to dynamically fetch models
	page, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]llm.ModelInfo, 0, len(page.Data))

	for _, m := range page.Data {
		id := m.ID
		// Skip models that don't support chat completions or responses API
		if strings.HasPrefix(id, "dall-e") ||
			strings.HasPrefix(id, "tts-") ||
			strings.HasPrefix(id, "whisper-") ||
			strings.HasPrefix(id, "text-embedding") ||
			strings.HasPrefix(id, "omni-moderation") ||
			strings.HasPrefix(id, "davinci") ||
			strings.HasPrefix(id, "babbage") ||
			strings.HasPrefix(id, "sora") ||
			strings.HasPrefix(id, "gpt-image") ||
			strings.Contains(id, "-tts") ||
			strings.Contains(id, "-transcribe") ||
			strings.Contains(id, "-realtime") ||
			strings.Contains(id, "computer-use") ||
			strings.HasSuffix(id, "-instruct") {
			continue
		}

		models = append(models, openAIModelInfo(id))
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })

	return models, nil
}

func responseMessageParam(role responses.EasyInputMessageRole, msg core.Message) *responses.EasyInputMessageParam {
	param := &responses.EasyInputMessageParam{Role: role}
	if len(msg.Images) == 0 {
		param.Content = responses.EasyInputMessageContentUnionParam{
			OfString: openai.Opt(msg.Content),
		}
		return param
	}

	content := make(responses.ResponseInputMessageContentListParam, 0, len(msg.Images)+1)
	if parts := core.InterleavedContentParts(msg); parts != nil {
		for _, p := range parts {
			switch p.Type {
			case core.ContentPartText:
				content = append(content, responses.ResponseInputContentParamOfInputText(p.Text))
			case core.ContentPartImage:
				content = append(content, responseImageContentPart(p.Image.MediaType, p.Image.Data))
			}
		}
	} else {
		for _, img := range msg.Images {
			content = append(content, responseImageContentPart(img.MediaType, img.Data))
		}
		if msg.Content != "" {
			content = append(content, responses.ResponseInputContentParamOfInputText(msg.Content))
		}
	}
	param.Content = responses.EasyInputMessageContentUnionParam{
		OfInputItemContentList: content,
	}
	return param
}

func responseImageContentPart(mediaType, data string) responses.ResponseInputContentUnionParam {
	part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
	if part.OfInputImage != nil {
		part.OfInputImage.ImageURL = openai.String(fmt.Sprintf("data:%s;base64,%s", mediaType, data))
	}
	return part
}

func messageHasResponseContent(msg core.Message) bool {
	return strings.TrimSpace(msg.Content) != "" || len(msg.Images) > 0
}

// Ensure Client implements Provider
var _ llm.Provider = (*Client)(nil)
