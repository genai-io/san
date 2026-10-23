// Side requests: one-off model calls answered from the conversation as it
// stands. A side request reuses the main agent's system prompt, tools and chain,
// so the provider's prompt cache serves the prefix; neither the prompt nor the
// reply enters the agent's chain, so the main agent never sees them. /btw,
// Suggest and Recap are side requests.
package app

import (
	"context"
	"slices"

	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
)

// sidePending answers a call the main agent is still running, so the request
// is well-formed and the model knows the result isn't in yet.
const sidePending = "[No result yet: this call is still running in the main conversation.]"

// sideReplying stands in for the reply the main agent is still writing. Left
// open, the request it answers reads as the model's own to do, and a model
// asked a side question then does both.
const sideReplying = "[The main agent is replying to this right now; its reply is not in yet.]"

// sideRequest is a side request captured on the UI goroutine, ready to send
// from any other.
type sideRequest struct {
	label   string // names the request in the debug log
	client  *llm.Client
	system  string
	schemas []core.ToolSchema
	chain   []core.Message
	prompt  string
}

// newSideRequest captures the main agent's prefix with prompt to follow it.
// The tools are sent though none may run: a request without them is a
// different prefix, and the cache would miss.
func (m *model) newSideRequest(label, prompt string) sideRequest {
	r := sideRequest{label: label, client: m.buildLLMClient(), chain: m.services.Agent.Messages(), prompt: prompt}
	if sys, tools := m.services.Agent.System(), m.services.Agent.Tools(); sys != nil && tools != nil {
		r.system, r.schemas = sys.Prompt(), tools.Schemas()
	} else {
		params := m.promptParams()
		r.system, r.schemas = params.System().Prompt(), params.ToolSchemas()
	}
	return r
}

// complete sends the request. A tool call in the reply is never run.
func (r sideRequest) complete(ctx context.Context) (llm.CompletionResponse, error) {
	msgs := r.client.RequestMessages(withSidePrompt(r.chain, r.prompt))
	resp, err := r.client.Complete(ctx, r.system, msgs, r.schemas, 0)
	if err == nil {
		log.Logger().Debug("side request answered", zap.String("label", r.label), zap.Int("input", resp.Usage.Input), zap.Int("cache_read", resp.Usage.CacheRead), zap.Int("cache_write", resp.Usage.CacheWrite), zap.Int("output", resp.Usage.Output))
	}
	return resp, err
}

// withSidePrompt appends prompt to a copy of chain, filling whatever the main
// agent has in flight with a placeholder: sidePending for a running tool call,
// sideReplying for a reply not yet written. The prompt is a message of its
// own: sdk-go's OpenAI chat driver sends a message holding tool results as
// tool messages only, dropping any text beside them.
func withSidePrompt(chain []core.Message, prompt string) []core.Message {
	msgs := slices.Clone(chain)
	if n := len(msgs); n > 0 && msgs[n-1].Role == ai.RoleUser {
		msgs = append(msgs, core.AssistantMessage(sideReplying, "", nil))
	} else if n > 0 {
		var pending []ai.ToolResult
		for _, call := range msgs[n-1].ToolCalls() {
			pending = append(pending, ai.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Content: ai.TextContent(sidePending)})
		}
		if len(pending) > 0 {
			msgs = append(msgs, ai.ToolResultsMessage(pending...))
		}
	}
	return append(msgs, core.UserMessage(prompt, nil))
}
