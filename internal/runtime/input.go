package runtime

import (
	"errors"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

// PendingInput owns native input until delivery or discard.
type PendingInput struct {
	core.Message
	FromUser    bool                // Use the user input admission and Recall path.
	Synthetic   bool                // Scheduled or generated input routed through that path, not submitted by the user.
	Completion  *session.Completion // Records the model call that generated this input; finishes on delivery or discard.
	Source      *session.EventRef   // Child event whose result is being delivered.
	Destination *session.Session    // Original receiving owner; required with Source.
	MemoryReady bool                // Preparation finished for this user input.
	RecallQuery string              // Original user text when submission was expanded by a host.
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
func (q *InputQueue) Discard() error {
	var err error
	for _, input := range q.items {
		err = errors.Join(err, input.Completion.Close())
	}
	q.items = nil
	q.cancelling = nil
	return err
}

// ResetAdmission clears stop policy after the runtime has been stopped.
func (q *InputQueue) ResetAdmission() {
	q.cancelling = nil
	q.held = false
}

func (q *InputQueue) ReplaceFirst(input PendingInput) { q.items[0] = input }

func (q *InputQueue) PopFirst() {
	q.items[0] = PendingInput{}
	q.items = q.items[1:]
}

// DeliverFirst attempts admission of the prepared head. Rejected recorded
// results are discarded; other rejected input stays queued.
func (q *InputQueue) DeliverFirst(send func(PendingInput) error, prepare func(core.Message) core.Message) error {
	if !q.CanDeliver(false) || len(q.items) == 0 {
		return nil
	}
	next := &q.items[0]
	if next.FromUser && next.RecallQuery == "" {
		next.RecallQuery = next.Message.Text()
	}
	next.Message = prepare(next.Message)
	if err := send(*next); err != nil {
		return q.rejectFirst(err)
	}
	q.PopFirst()
	return nil
}

func (q *InputQueue) rejectFirst(err error) error {
	next := q.items[0]
	if next.Completion != nil || next.Source != nil || next.Destination != nil {
		if closeErr := next.Completion.Close(); closeErr != nil {
			err = errors.Join(err, &llm.RecordingError{Err: closeErr})
		}
		q.PopFirst()
	}
	return err
}
