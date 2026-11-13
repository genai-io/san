package agent

import (
	"errors"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

// PendingInput owns native input until delivery or discard.
type PendingInput struct {
	core.Message
	FromUser   bool
	Completion *session.Completion // Records the model call that generated this input; finishes on delivery or discard.
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

// Deliver sends FIFO; rejected recorded results are discarded, user input is retained.
func (q *InputQueue) Deliver(send func(PendingInput) error, prepare func(core.Message) core.Message, recall func(string) error) error {
	if !q.CanDeliver(false) {
		return nil
	}
	for len(q.items) > 0 {
		next := &q.items[0]
		var err error
		if next.FromUser {
			err = recall(next.Text())
		}
		if err == nil {
			next.Message = prepare(next.Message)
			err = send(*next)
		}
		if err != nil {
			if next.Completion != nil {
				if closeErr := next.Completion.Close(); closeErr != nil {
					err = errors.Join(err, &llm.RecordingError{Err: closeErr})
				}
				q.items = q.items[1:]
			}
			return err
		}
		q.items = q.items[1:]
	}
	return nil
}
