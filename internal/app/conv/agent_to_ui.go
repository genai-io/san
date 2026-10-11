package conv

import (
	"context"
	"fmt"
	"sync"
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

// AgentActivityBatchMsg applies a whole poll without a timer per activity line.
type AgentActivityBatchMsg []AgentActivityMsg

type pendingActivity struct {
	lines   []string
	omitted int
}

// AgentToUI transports per-worker activity, questions, and masked secret prompts.
// The agent side sends; the TUI polls Check. Activity never blocks on a user prompt.
type AgentToUI struct {
	mu      sync.Mutex
	pending map[string]*pendingActivity
	order   []string
	limit   int // Per-worker recent lines; a noisy sibling cannot evict another.
	qch     chan QuestionRequestMsg
	sch     chan SecretPromptRequestMsg
}

// NewAgentToUI creates an AgentToUI with the given buffer size.
func NewAgentToUI(buffer int) *AgentToUI {
	if buffer <= 0 {
		buffer = 100
	}
	return &AgentToUI{
		pending: make(map[string]*pendingActivity),
		limit:   buffer,
		qch:     make(chan QuestionRequestMsg, buffer),
		sch:     make(chan SecretPromptRequestMsg, buffer),
	}
}

// SendForToolCall enqueues an activity line for a specific tool call.
func (h *AgentToUI) SendForToolCall(toolCallID string, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.pending[toolCallID]
	if pending == nil {
		pending = &pendingActivity{}
		h.pending[toolCallID] = pending
		h.order = append(h.order, toolCallID)
	}
	before := len(pending.lines)
	pending.lines = retainAgentActivityLimit(pending.lines, msg, h.limit)
	if activityMetadataKey(msg) == "" && len(pending.lines) <= before {
		pending.omitted++
	}
}

// takeActivity drains a bounded snapshot in one poll rather than one line per
// 100ms. Metadata is coalesced, recent history is bounded per worker, and the
// newest state always survives a burst. Questions use their own channels.
func (h *AgentToUI) takeActivity() AgentActivityBatchMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	var batch AgentActivityBatchMsg
	for _, id := range h.order {
		pending := h.pending[id]
		if pending.omitted > 0 {
			batch = append(batch, AgentActivityMsg{Index: -1, ToolCallID: id, Message: fmt.Sprintf("Omitted: %d", pending.omitted)})
		}
		for _, line := range pending.lines {
			batch = append(batch, AgentActivityMsg{Index: -1, ToolCallID: id, Message: line})
		}
		delete(h.pending, id)
	}
	h.order = nil
	return batch
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
		default:
		}
		if batch := h.takeActivity(); len(batch) > 0 {
			return batch
		}
		return AgentToUITickMsg{}
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

// maxAgentActivityHistory is the maximum number of activity lines retained per agent.
const maxAgentActivityHistory = 12
