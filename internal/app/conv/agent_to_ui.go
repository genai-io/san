package conv

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/tool"
)

// AgentActivityMsg carries one activity line from a running agent — its model,
// its mode, a tool it is calling, "Thinking...", or token usage. Agent lines
// accumulate into the per-agent activity feed; a non-agent tool call (Bash)
// instead sends its latest output counter, which the receiver shows inline on
// the call's row (replacing the previous value, not appending).
type AgentActivityMsg struct {
	Index      int
	ToolCallID string
	Message    string
}

// AgentToUITickMsg is the idle heartbeat: Check emits it when no message is
// ready, so the poll re-arms without blocking.
type AgentToUITickMsg struct{}

// AgentToUI is the instance-scoped transport from running agents and tools
// to the TUI. It multiplexes three kinds of traffic: activity lines to display,
// interactive questions to ask (AskUser lands here), and masked secret prompts
// (RequestSecret lands here). The agent side sends; the TUI side polls Check.
type AgentToUI struct {
	ch  chan AgentActivityMsg
	qch chan QuestionRequestMsg
	sch chan SecretPromptRequestMsg
}

// NewAgentToUI creates an AgentToUI with the given buffer size.
func NewAgentToUI(buffer int) *AgentToUI {
	if buffer <= 0 {
		buffer = 100
	}
	return &AgentToUI{
		ch:  make(chan AgentActivityMsg, buffer),
		qch: make(chan QuestionRequestMsg, buffer),
		sch: make(chan SecretPromptRequestMsg, buffer),
	}
}

// SendForAgent enqueues an activity line for a specific agent index.
func (h *AgentToUI) SendForAgent(index int, msg string) {
	select {
	case h.ch <- AgentActivityMsg{Index: index, Message: msg}:
	default:
	}
}

// SendForToolCall enqueues an activity line for a specific tool call.
func (h *AgentToUI) SendForToolCall(toolCallID string, msg string) {
	select {
	case h.ch <- AgentActivityMsg{Index: -1, ToolCallID: toolCallID, Message: msg}:
	default:
	}
}

// Ask enqueues an interactive question and waits for the user's response.
func (h *AgentToUI) Ask(ctx context.Context, index int, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
	if h == nil {
		return nil, fmt.Errorf("agent-UI channel not initialized")
	}

	reply := make(chan *tool.QuestionResponse, 1)
	select {
	case h.qch <- QuestionRequestMsg{Index: index, Request: req, Reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case resp := <-reply:
		if resp == nil {
			return nil, fmt.Errorf("question prompt closed without a response")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RequestSecret enqueues a masked secret prompt and waits for the user's
// response. The returned value is kept out of transcripts and model messages.
func (h *AgentToUI) RequestSecret(ctx context.Context, prompt string) (string, bool, error) {
	if h == nil {
		return "", false, fmt.Errorf("agent-UI channel not initialized")
	}

	reply := make(chan SecretPromptResponse, 1)
	select {
	case h.sch <- SecretPromptRequestMsg{Prompt: prompt, Reply: reply}:
	case <-ctx.Done():
		return "", false, ctx.Err()
	}

	select {
	case resp := <-reply:
		if resp.Cancelled {
			return "", false, nil
		}
		return resp.Value, true, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

// Check returns a tea.Cmd that polls this channel for the next update.
func (h *AgentToUI) Check() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		select {
		case q := <-h.qch:
			return q
		case s := <-h.sch:
			return s
		case u := <-h.ch:
			return u
		default:
			return AgentToUITickMsg{}
		}
	})
}

// DrainPendingQuestions cancels any pending questions left in the channel.
// Called when the agent stops to prevent orphaned questions from appearing later.
func (h *AgentToUI) DrainPendingQuestions() {
	if h == nil {
		return
	}
	for {
		select {
		case q := <-h.qch:
			select {
			case q.Reply <- &tool.QuestionResponse{Cancelled: true}:
			default:
			}
		case s := <-h.sch:
			select {
			case s.Reply <- SecretPromptResponse{Cancelled: true}:
			default:
			}
		default:
			return
		}
	}
}

// Drain pulls all pending activity lines into taskActivity.
func (h *AgentToUI) Drain(taskActivity map[int][]string) map[int][]string {
	for {
		select {
		case u := <-h.ch:
			if taskActivity == nil {
				taskActivity = make(map[int][]string)
			}
			taskActivity[u.Index] = append(taskActivity[u.Index], u.Message)
			if len(taskActivity[u.Index]) > maxAgentActivityHistory {
				taskActivity[u.Index] = taskActivity[u.Index][len(taskActivity[u.Index])-maxAgentActivityHistory:]
			}
		default:
			return taskActivity
		}
	}
}

// maxAgentActivityHistory is the maximum number of activity lines retained per agent.
const maxAgentActivityHistory = 12
