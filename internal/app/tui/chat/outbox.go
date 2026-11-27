package chat

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
)

type AgentOutboxMsg struct {
	Event  core.Event
	Batch  []core.Event // set when multiple events were drained at once
	Closed bool
	Outbox <-chan core.Event // the outbox it was read from
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
