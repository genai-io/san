package conv

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type AgentOutboxMsg struct {
	Event  core.Event
	Batch  []core.Event // set when multiple events were drained at once
	Closed bool
	Outbox <-chan core.Event // the outbox it was read from
}

// Runtime defines what the conv event handlers need from the root model. Each
// method represents a coherent operation (not a fine-grained primitive),
// keeping the interface small and each implementation substantial.
//
// The prefix says where a member comes from:
//
//   - On*     — the agent did something. One per agent event, and they arrive
//     in the outbox's order, so they have a fixed place on the turn's timeline.
//   - Handle* — a system message arrived: a background command's result, a
//     timer, a side channel. No place on that timeline; it can land any time.
//   - a verb  — not a callback at all, but a service conv calls on the host.
type Runtime interface {
	// ── On*: agent lifecycle ────────────────────────────────────
	OnInference(resp *ai.Response) // PostInfer
	// OnToolResult hands back the result message the model is told; the
	// structured form the interface draws arrives separately, on ToolEnd.
	OnToolResult(tr core.ToolResult) *core.ToolResult
	// OnStepEnd fires when a tool batch completes and the turn continues, so
	// pending work (one queued user message) can reach the
	// agent in time for its next step.
	OnStepEnd() tea.Cmd
	OnTurnEnd(result core.Result) tea.Cmd
	OnAgentStop(err error) tea.Cmd
	OnCompactStart(count int) tea.Cmd
	OnCompactEnd(end sdkagent.CompactionEnd) tea.Cmd
	OnCompacted(info core.Compacted) tea.Cmd

	// ── Handle*: system messages ────────────────────────────────
	HandlePermGate(req *agent.PermGateRequest) tea.Cmd
	HandleCompactResult(msg CompactResultMsg) tea.Cmd

	// ── Services conv calls on the host ─────────────────────────
	CommitMessages() []tea.Cmd
	FlushStreamingBlocks() []tea.Cmd
	// TakeReviewDecision consumes the auto-review decision stashed for a tool
	// call (nil if it was not auto-reviewed), to stamp onto its rendered result.
	TakeReviewDecision(callID string) *core.ReviewDecision
	HasRunningTasks() bool
	// AgentOutbox is the selected runtime's outbox, retained through its final events.
	AgentOutbox() <-chan core.Event

	// ── Transport lifetime ──────────────────────────────────────
	// ListenForAgentEvents waits for the next batch of agent events.
	// conv re-arms after ordinary events; OnTurnEnd and OnCompacted re-arm
	// at their boundaries. OnAgentStop leaves the listener stopped.
	ListenForAgentEvents() tea.Cmd
}

// DrainAgentOutbox blocks until at least one event is available, then greedily
// drains additional ready events to reduce Update+View cycles. Stops at
// terminal events (OnTurn/OnStop/OnCompact) so turn boundaries aren't crossed.
func DrainAgentOutbox(outbox <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		msg := drainOutbox(outbox)
		msg.Outbox = outbox
		return msg
	}
}

func drainOutbox(outbox <-chan core.Event) AgentOutboxMsg {
	ev, ok := <-outbox
	if !ok {
		return AgentOutboxMsg{Closed: true}
	}
	if isTerminalEvent(ev) {
		return AgentOutboxMsg{Event: ev}
	}
	batch := []core.Event{ev}
	for {
		select {
		case next, ok := <-outbox:
			if !ok {
				return AgentOutboxMsg{Batch: batch, Closed: true}
			}
			batch = append(batch, next)
			if isTerminalEvent(next) {
				return AgentOutboxMsg{Batch: batch}
			}
		default:
			if len(batch) == 1 {
				return AgentOutboxMsg{Event: batch[0]}
			}
			return AgentOutboxMsg{Batch: batch}
		}
	}
}

// isTerminalEvent says a batch should be flushed to the UI now rather than
// waiting for more.
func isTerminalEvent(ev core.Event) bool {
	switch ev.(type) {
	case core.TurnEnded, core.AgentStopped, core.Compacted:
		return true
	}
	return false
}
