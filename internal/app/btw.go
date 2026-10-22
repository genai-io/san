// /btw: a side question answered from the conversation as it stands. The
// request reuses the main agent's system prompt, tools and chain, so the
// provider's prompt cache serves the prefix; neither the question nor the
// answer enters the agent's chain, so the main agent never sees them.
package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
)

const btwTimeout = 2 * time.Minute

const btwInstructions = `<system-reminder>This is a side question from the user. Answer it directly in a single response from what this conversation already holds.
- You are a separate, one-off answer: the main work is not interrupted and continues on its own, and there is no follow-up turn.
- Answer only this question. Requests earlier in the conversation are the main agent's to carry out, not yours.
- Do not frame it as an interruption or mention what you were previously doing.
- You cannot use tools here, and nothing written as a tool call runs. If answering needs reading files, running commands or searching, say it can't be checked from a side question and suggest asking in the main conversation.
- If you don't know, say so; never promise to look it up.
</system-reminder>

`

// btwPending answers a call the main agent is still running, so the request
// is well-formed and the model knows the result isn't in yet.
const btwPending = "[No result yet: this call is still running in the main conversation.]"

// btwReplying stands in for the reply the main agent is still writing. Left
// open, the request it answers reads as the model's own to do, and a model
// asked a side question then does both.
const btwReplying = "[The main agent is replying to this right now; its reply is not in yet.]"

// btwAsk is one /btw in flight, drawn live until its answer lands. It keeps
// the session it was asked in: an answer that outlives a /clear or /resume
// belongs to the conversation it was asked about, not the one now on screen.
type btwAsk struct {
	id        int64
	question  string
	sessionID string
}

type btwAnswerMsg struct {
	ask    btwAsk
	answer string
}

// sideQuestion starts answering question off the UI goroutine.
func (m *model) sideQuestion(question string) tea.Cmd {
	client := m.buildLLMClient()
	var system string
	var schemas []core.ToolSchema
	if sys, tools := m.services.Agent.System(), m.services.Agent.Tools(); sys != nil && tools != nil {
		system, schemas = sys.Prompt(), tools.Schemas()
	} else {
		params := m.promptParams()
		system, schemas = params.System().Prompt(), params.Schemas()
	}
	msgs := client.RequestMessages(withSideQuestion(m.services.Agent.Messages(), question))
	ask := btwAsk{id: time.Now().UnixNano(), question: strings.Join(strings.Fields(question), " "), sessionID: m.sessionID()}
	// A running spinner already ticks; a second chain would double its speed.
	var tick tea.Cmd
	if !m.needsSpinner() {
		tick = m.conv.Spinner.Tick
	}
	m.btw = append(m.btw, ask)
	return tea.Batch(tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), btwTimeout)
		defer cancel()
		return btwAnswerMsg{ask: ask, answer: askSide(ctx, client, system, schemas, msgs)}
	})
}

func (m *model) onBtwAnswer(msg btwAnswerMsg) tea.Cmd {
	m.btw = slices.DeleteFunc(m.btw, func(a btwAsk) bool { return a.id == msg.ask.id })
	if msg.ask.sessionID != m.sessionID() {
		return nil
	}
	// The question is one line, so the first blank line splits it from the answer.
	m.conv.AddNotice("/btw " + msg.ask.question + "\n\n" + msg.answer)
	return tea.Batch(m.CommitMessages()...)
}

// btwLive draws this session's questions still waiting on an answer.
func (m *model) btwLive() string {
	var rows []string
	for _, a := range m.btw {
		if a.sessionID == m.sessionID() {
			rows = append(rows, conv.RenderSideAnswer("/btw "+a.question+"\n\n"+m.conv.Spinner.View()+" answering…", m.env.Width, nil))
		}
	}
	return strings.TrimSuffix(strings.Join(rows, "\n"), "\n")
}

// withSideQuestion appends the question to a copy of chain, filling whatever
// the main agent has in flight with a placeholder: btwPending for a running
// tool call, btwReplying for a reply not yet written. The question is a
// message of its own: the OpenAI chat driver sends a message holding tool
// results as tool messages only, dropping any text beside them.
func withSideQuestion(chain []core.Message, question string) []core.Message {
	msgs := slices.Clone(chain)
	if n := len(msgs); n > 0 && msgs[n-1].Role == ai.RoleUser {
		msgs = append(msgs, core.AssistantMessage(btwReplying, "", nil))
	} else if n > 0 && msgs[n-1].Role == ai.RoleAssistant {
		var pending ai.Content
		for _, b := range msgs[n-1].Content {
			if b.Type == ai.BlockToolCall && b.ToolCall != nil {
				pending = append(pending, ai.ToolResultBlock(ai.ToolResult{ToolCallID: b.ToolCall.ID, ToolName: b.ToolCall.Name, Content: ai.TextContent(btwPending)}))
			}
		}
		if len(pending) > 0 {
			msgs = append(msgs, core.Message{Role: ai.RoleUser, Content: pending})
		}
	}
	return append(msgs, core.UserMessage(btwInstructions+question, nil))
}

func askSide(ctx context.Context, client *llm.Client, system string, schemas []core.ToolSchema, msgs []core.Message) string {
	tc, err := client.TurnClient(msgs)
	if err != nil {
		return "Error: " + err.Error()
	}
	opts := append([]ai.Option{ai.WithSystem(system), ai.WithTools(schemas...)}, client.CallOptions()...)
	resp, err := tc.Complete(ctx, msgs, opts...)
	if err != nil {
		return "Error: " + err.Error()
	}
	log.Logger().Debug("btw answered", zap.Int("input", resp.Usage.Input), zap.Int("cache_read", resp.Usage.CacheRead), zap.Int("cache_write", resp.Usage.CacheWrite), zap.Int("output", resp.Usage.Output))
	if text := strings.TrimSpace(resp.Content.Text()); text != "" {
		return text
	}
	for _, b := range resp.Content {
		if b.Type == ai.BlockToolCall && b.ToolCall != nil {
			return fmt.Sprintf("(The model tried to call %s instead of answering. Ask in the main conversation.)", b.ToolCall.Name)
		}
	}
	return "(No answer.)"
}
