package sidecard

import (
	"context"
	"slices"

	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
)

const (
	sidePending  = "[No result yet: this call is still running in the main conversation.]"
	sideReplying = "[The main agent is replying to this right now; its reply is not in yet.]"
)

// Snapshot is a captured conversation prefix and one side prompt. Schemas may
// be included for cache reuse, but there is no tool execution path.
type Snapshot struct {
	client    *llm.Client
	system    string
	schemas   []core.ToolSchema
	messages  []core.Message
	recording *session.Completion
}

// SnapshotOptions lets the host select a model client and an explicit context
// prefix. Providing schemas preserves the prefix without granting tool access.
type SnapshotOptions struct {
	Client   *llm.Client
	System   string
	Tools    []core.ToolSchema
	Messages []core.Message
	Owner    *session.Session
	Purpose  string
}

// NewSnapshot captures the supplied conversation and appends a separate side
// prompt, closing an in-flight turn with a placeholder if necessary.
func NewSnapshot(opts SnapshotOptions, prompt string, extra ...string) Snapshot {
	messages := withSidePrompt(opts.Messages, prompt, extra...)
	for i := range messages {
		messages[i].Content = messages[i].Content.Clone()
		if i >= len(opts.Messages) && messages[i].ID == "" {
			messages[i].ID = core.NewMessageID()
		}
	}
	return Snapshot{
		client: opts.Client, system: opts.System, schemas: slices.Clone(opts.Tools), messages: messages,
		recording: opts.Owner.Completion(opts.Purpose, messages[len(opts.Messages):]...),
	}
}

// Recording is resolved by the domain owner, not by the inference runner.
func (s Snapshot) Recording() *session.Completion { return s.recording }

// Complete retains the client defaults, provider message adaptation, and
// transient retry policy of conversation-based side calls.
func (s Snapshot) Complete(ctx context.Context) (llm.CompletionResponse, error) {
	return s.client.Complete(ctx, s.system, s.client.RequestMessages(s.messages), s.schemas, 0, s.recording.Admission())
}

// withSidePrompt closes an in-flight turn with a placeholder before adding the
// side prompt. The prompt must be separate: the OpenAI chat driver drops text
// beside tool results.
func withSidePrompt(chain []core.Message, prompt string, extra ...string) []core.Message {
	msgs := append(make([]core.Message, 0, len(chain)+2), chain...)
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
	msg := core.UserMessage(prompt, nil)
	msg.Content = reminder.AppendBlocks(msg.Content, extra...)
	return append(msgs, msg)
}
