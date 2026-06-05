package google

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"slices"
	"strings"
	"sync"

	"google.golang.org/genai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	streamutil "github.com/genai-io/san/internal/llm/stream"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/secret"
)

// Client implements the Provider interface using the Google GenAI SDK
type Client struct {
	client       *genai.Client
	name         string
	modelsMu     sync.Mutex
	cachedModels []llm.ModelInfo
}

// NewClient creates a new Google client with the given SDK client
func NewClient(client *genai.Client, name string) *Client {
	return &Client{
		client: client,
		name:   name,
	}
}

// Name returns the provider name
func (c *Client) Name() string {
	return c.name
}

// Stream sends a completion request and returns a channel of streaming chunks
func (c *Client) Stream(ctx context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk)

	go func() {
		defer close(ch)

		// Strip orphan function_call / function_response pairs left by a
		// mid-stream cancel. Gemini rejects requests where a function_call
		// is not followed by a matching function_response.
		sanitized := sanitizeToolMessages(opts.Messages)

		// Convert messages to Google format
		contents := make([]*genai.Content, 0, len(sanitized))
		for _, msg := range sanitized {
			var role string
			switch msg.Role {
			case core.RoleUser:
				role = "user"
			case core.RoleAssistant:
				role = "model"
			default:
				role = string(msg.Role)
			}

			parts := make([]*genai.Part, 0, 2)

			if msg.ToolResult != nil {
				// Tool result as function response
				var result map[string]any
				if err := json.Unmarshal([]byte(msg.ToolResult.Content), &result); err != nil {
					// Wrap non-JSON content in a result object
					result = map[string]any{"result": msg.ToolResult.Content}
				}
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       msg.ToolResult.ToolCallID,
						Name:     msg.ToolResult.ToolName,
						Response: result,
					},
				})
			} else if len(msg.ToolCalls) > 0 {
				// Tool calls as function calls
				if msg.Content != "" {
					parts = append(parts, &genai.Part{Text: msg.Content})
				}
				for _, tc := range msg.ToolCalls {
					var args map[string]any
					if tc.Input != "" {
						if err := json.Unmarshal([]byte(tc.Input), &args); err != nil {
							args = nil
						}
					}
					p := &genai.Part{
						FunctionCall: &genai.FunctionCall{
							ID:   tc.ID,
							Name: tc.Name,
							Args: args,
						},
					}
					if len(tc.ThoughtSignature) > 0 {
						p.ThoughtSignature = tc.ThoughtSignature
					}
					parts = append(parts, p)
				}
			} else if len(msg.Images) > 0 {
				if contentParts := core.InterleavedContentParts(msg); contentParts != nil {
					for _, cp := range contentParts {
						switch cp.Type {
						case core.ContentPartText:
							parts = append(parts, &genai.Part{Text: cp.Text})
						case core.ContentPartImage:
							decoded, err := base64.StdEncoding.DecodeString(cp.Image.Data)
							if err != nil {
								log.Logger().Warn("skipping image: base64 decode failed")
								continue
							}
							parts = append(parts, &genai.Part{
								InlineData: &genai.Blob{
									MIMEType: cp.Image.MediaType,
									Data:     decoded,
								},
							})
						}
					}
				} else {
					for _, img := range msg.Images {
						decoded, err := base64.StdEncoding.DecodeString(img.Data)
						if err != nil {
							log.Logger().Warn("skipping image: base64 decode failed")
							continue
						}
						parts = append(parts, &genai.Part{
							InlineData: &genai.Blob{
								MIMEType: img.MediaType,
								Data:     decoded,
							},
						})
					}
					if msg.Content != "" {
						parts = append(parts, &genai.Part{Text: msg.Content})
					}
				}
			} else {
				parts = append(parts, &genai.Part{Text: msg.Content})
			}

			contents = append(contents, &genai.Content{
				Role:  role,
				Parts: parts,
			})
		}

		// Build config
		config := &genai.GenerateContentConfig{}

		if opts.SystemPrompt != "" {
			config.SystemInstruction = &genai.Content{
				Parts: []*genai.Part{{Text: opts.SystemPrompt}},
			}
		}

		if opts.MaxTokens > 0 {
			config.MaxOutputTokens = int32(opts.MaxTokens)
		}

		if opts.Temperature > 0 {
			temp := float32(opts.Temperature)
			config.Temperature = &temp
		}

		// Configure thinking
		if budget := googleThinkingBudget(opts.ThinkingEffort); budget != nil {
			config.ThinkingConfig = &genai.ThinkingConfig{
				IncludeThoughts: true,
				ThinkingBudget:  budget,
			}
		}

		// Add tools if provided
		if len(opts.Tools) > 0 {
			funcDecls := make([]*genai.FunctionDeclaration, 0, len(opts.Tools))
			for _, t := range opts.Tools {
				fd := &genai.FunctionDeclaration{
					Name:        t.Name,
					Description: t.Description,
				}
				// Use ParametersJsonSchema for JSON schema parameters
				if t.Parameters != nil {
					fd.ParametersJsonSchema = t.Parameters
				}
				funcDecls = append(funcDecls, fd)
			}
			config.Tools = []*genai.Tool{
				{FunctionDeclarations: funcDecls},
			}
		}

		// Log request
		log.LogRequestCtx(ctx, c.name, opts.Model, opts)

		state := streamutil.NewState(c.name)

		for result, err := range c.client.Models.GenerateContentStream(ctx, opts.Model, contents, config) {
			if err != nil {
				state.Fail(ctx, ch, err)
				return
			}
			state.Count()

			// Process candidates
			for _, candidate := range result.Candidates {
				if candidate.Content == nil {
					continue
				}

				for _, part := range candidate.Content.Parts {
					// Handle text (distinguish thinking from regular text)
					if part.Text != "" {
						if part.Thought {
							state.EmitThinking(ctx, ch, part.Text)
						} else {
							state.EmitText(ctx, ch, part.Text)
						}
					}

					// Handle function calls
					if part.FunctionCall != nil {
						fc := part.FunctionCall
						argsJSON, _ := json.Marshal(fc.Args)

						state.EmitToolStart(ctx, ch, fc.ID, fc.Name)
						state.EmitToolInput(ctx, ch, fc.ID, string(argsJSON))

						state.Response.ToolCalls = append(state.Response.ToolCalls, core.ToolCall{
							ID:               fc.ID,
							Name:             fc.Name,
							Input:            string(argsJSON),
							ThoughtSignature: part.ThoughtSignature,
						})
					}
				}

				// Handle finish reason
				if candidate.FinishReason != "" {
					switch candidate.FinishReason {
					case "STOP":
						state.Response.StopReason = "end_turn"
					case "MAX_TOKENS":
						state.Response.StopReason = "max_tokens"
					default:
						state.Response.StopReason = string(candidate.FinishReason)
					}
				}
			}

			// Handle usage
			if result.UsageMetadata != nil {
				state.UpdateUsage(int(result.UsageMetadata.PromptTokenCount), int(result.UsageMetadata.CandidatesTokenCount))
			}
		}

		state.EnsureToolUseStopReason()
		state.Finish(ctx, ch)
	}()

	return ch
}

// ListModels returns the available models for Google using the API.
// Results are cached after a successful fetch; a failed fetch (e.g. cancelled
// context) is not cached so subsequent calls can retry.
func (c *Client) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	c.modelsMu.Lock()
	defer c.modelsMu.Unlock()

	if c.cachedModels != nil {
		return c.cachedModels, nil
	}

	models, err := c.fetchModels(ctx)
	if err != nil {
		return nil, err
	}
	c.cachedModels = models
	return c.cachedModels, nil
}

// fetchModels fetches available Gemini models from the Google API.
func (c *Client) fetchModels(ctx context.Context) ([]llm.ModelInfo, error) {
	models := make([]llm.ModelInfo, 0, 16)

	for m, err := range c.client.Models.All(ctx) {
		if err != nil {
			return nil, err
		}

		// Filter for Gemini models
		name := m.Name
		if strings.Contains(name, "gemini") {
			// Extract short model ID from full name (e.g., "models/gemini-2.0-flash" -> "gemini-2.0-flash")
			id, _ := strings.CutPrefix(name, "models/")

			// Skip deprecated/experimental models for cleaner display
			if strings.Contains(id, "-exp") || strings.Contains(id, "-latest") {
				continue
			}

			displayName := m.DisplayName
			if displayName == "" {
				displayName = id
			}

			models = append(models, llm.ModelInfo{
				ID:               id,
				Name:             displayName,
				DisplayName:      displayName,
				InputTokenLimit:  int(m.InputTokenLimit),
				OutputTokenLimit: int(m.OutputTokenLimit),
			})
		}
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no Gemini models found from API")
	}

	slices.SortFunc(models, func(a, b llm.ModelInfo) int { return cmp.Compare(a.ID, b.ID) })

	return models, nil
}

// NewAPIKeyClient creates a new Google client using API Key authentication
func NewAPIKeyClient(ctx context.Context) (llm.Provider, error) {
	apiKey := secret.Resolve("GOOGLE_API_KEY")
	if apiKey == "" {
		apiKey = secret.Resolve("GEMINI_API_KEY")
	}

	// The Google GenAI SDK warns via log.Printf when both GOOGLE_API_KEY and
	// GEMINI_API_KEY are set. Suppress it to prevent leaking into the TUI.
	w := stdlog.Writer()
	stdlog.SetOutput(io.Discard)
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	stdlog.SetOutput(w)
	if err != nil {
		return nil, err
	}

	return NewClient(client, "google:api_key"), nil
}

// sanitizeToolMessages drops orphan function_call / function_response pairs.
// Mirrors openaicompat.SanitizeToolMessages and anthropic.sanitizeToolResults:
// Gemini's API rejects requests where an assistant function_call is not
// followed by a matching function_response (the user-initiated cancel path
// can leave such orphans in agent.messages).
func sanitizeToolMessages(msgs []core.Message) []core.Message {
	result := make([]core.Message, 0, len(msgs))

	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]

		if msg.ToolResult != nil {
			// Tool results are only valid immediately after their assistant call.
			continue
		}

		if msg.Role != core.RoleAssistant || len(msg.ToolCalls) == 0 {
			result = append(result, msg)
			continue
		}

		j := i + 1
		var followingResults []core.Message
		for j < len(msgs) && msgs[j].ToolResult != nil {
			followingResults = append(followingResults, msgs[j])
			j++
		}

		resultIDs := make(map[string]bool, len(followingResults))
		for _, r := range followingResults {
			resultIDs[r.ToolResult.ToolCallID] = true
		}

		filteredCalls := make([]core.ToolCall, 0, len(msg.ToolCalls))
		callIDs := make(map[string]bool, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if resultIDs[tc.ID] {
				filteredCalls = append(filteredCalls, tc)
				callIDs[tc.ID] = true
			}
		}

		msg.ToolCalls = filteredCalls
		if len(msg.ToolCalls) > 0 || msg.Content != "" {
			result = append(result, msg)
		}

		seenResults := make(map[string]bool, len(followingResults))
		for _, r := range followingResults {
			id := r.ToolResult.ToolCallID
			if callIDs[id] && !seenResults[id] {
				result = append(result, r)
				seenResults[id] = true
			}
		}

		i = j - 1
	}

	return result
}

func googleThinkingBudget(effort string) *int32 {
	var b int
	switch effort {
	case "think":
		b = 5000
	case "think+":
		b = 32000
	case "ultrathink":
		b = 128000
	default:
		return nil
	}
	budget := int32(b)
	return &budget
}

func (c *Client) ThinkingEfforts(model string) []string {
	return []string{"off", "think", "think+", "ultrathink"}
}

func (c *Client) DefaultThinkingEffort(model string) string {
	return "off"
}

// Ensure Client implements Provider
var _ llm.Provider = (*Client)(nil)
