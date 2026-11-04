package agent

import "github.com/genai-io/san/internal/core"

// PendingInput marks user input for memory recall.
type PendingInput struct {
	core.Message
	FromUser bool
	// GroupSkill records an explicit user invocation across queued delivery.
	GroupSkill bool
}

// InputQueue owns unsent input and cancellation admission. The caller serializes access.
type InputQueue struct {
	items      []PendingInput
	held       bool
	cancelling *core.TurnFinalization
}

func (q *InputQueue) Enqueue(input PendingInput) {
	q.items = append(q.items, input)
	if input.FromUser {
		q.held = false
	}
}

func (q *InputQueue) Len() int { return len(q.items) }

func (q *InputQueue) First() PendingInput {
	if len(q.items) == 0 {
		return PendingInput{}
	}
	return q.items[0]
}

func (q *InputQueue) Held() bool { return q.held }

func (q *InputQueue) Cancelling() *core.TurnFinalization { return q.cancelling }

// CanDeliver never lets explicit continuation bypass consumer cleanup.
func (q *InputQueue) CanDeliver(explicit bool) bool {
	return q.cancelling == nil && (!q.held || explicit)
}

// Hold parks automatic input even when there is no active turn to cancel.
func (q *InputQueue) Hold(receipt *core.TurnFinalization) bool {
	q.held = true
	if receipt == nil || receipt == q.cancelling {
		return false
	}
	q.cancelling = receipt
	return true
}

// Finalize releases the matching fence after receipt.Wait, not the manual-stop hold.
func (q *InputQueue) Finalize(receipt *core.TurnFinalization) bool {
	if receipt == nil || receipt != q.cancelling {
		return false
	}
	q.cancelling = nil
	return true
}

// Discard retires pending input and its fence without granting continuation.
func (q *InputQueue) Discard() {
	q.items = nil
	q.cancelling = nil
}

// ResetAdmission clears stop policy after the runtime has been stopped.
func (q *InputQueue) ResetAdmission() {
	q.cancelling = nil
	q.held = false
}

// Deliver sends FIFO, retaining rejected input. Retries rerun prepare and user recall.
func (q *InputQueue) Deliver(send func(core.Message) error, prepare func(core.Message) core.Message, recall func(string)) error {
	return q.DeliverEligible(send, prepare, recall, func(PendingInput) bool { return true })
}

// DeliverEligible sends FIFO until canDeliver returns false, retaining that input and everything after it.
func (q *InputQueue) DeliverEligible(send func(core.Message) error, prepare func(core.Message) core.Message, recall func(string), canDeliver func(PendingInput) bool) error {
	if !q.CanDeliver(false) {
		return nil
	}
	for len(q.items) > 0 {
		next := &q.items[0]
		if !canDeliver(*next) {
			return nil
		}
		if next.FromUser {
			recall(next.Text())
		}
		next.Message = prepare(next.Message)
		if err := send(next.Message); err != nil {
			return err
		}
		q.items = q.items[1:]
	}
	return nil
}
