package conv

import (
	"context"
	"fmt"
	"sync"
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
	mu       sync.Mutex
	pending  map[string]*pendingActivity
	order    []string
	limit    int // Per-worker recent lines; a noisy sibling cannot evict another.
	ctx      context.Context
	cancel   context.CancelFunc
	requests chan *InteractionRequest
}

// NewAgentToUI creates an AgentToUI with the given buffer size.
func NewAgentToUI(buffer int) *AgentToUI {
	if buffer <= 0 {
		buffer = 100
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &AgentToUI{
		pending: make(map[string]*pendingActivity), limit: buffer,
		ctx: ctx, cancel: cancel, requests: make(chan *InteractionRequest, buffer),
	}
}

// SendForToolCall enqueues an activity line for a specific tool call.
func (h *AgentToUI) SendForToolCall(toolCallID string, msg string) {
	if h == nil || h.ctx.Err() != nil {
		return
	}
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

// maxAgentActivityHistory is the maximum number of activity lines retained per agent.
const maxAgentActivityHistory = 12
