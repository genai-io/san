package agent

import "github.com/genai-io/san/internal/core"

// PendingInput distinguishes user prompts from harness continuations for memory recall.
type PendingInput struct {
	core.Message
	FromUser bool
}

// InputQueue retains admitted native input until Runtime.Send accepts it.
// The application owner serializes access and applies lifecycle gates before delivery.
type InputQueue []PendingInput

// Deliver preserves FIFO order and prepared input on rejection. Preparation and
// recall run on each attempt; harness messages never replace the user's recall.
func (q *InputQueue) Deliver(send func(core.Message) error, prepare func(core.Message) core.Message, recall func(string)) error {
	for len(*q) > 0 {
		next := &(*q)[0]
		if next.FromUser {
			recall(next.Text())
		}
		next.Message = prepare(next.Message)
		if err := send(next.Message); err != nil {
			return err
		}
		*q = (*q)[1:]
	}
	return nil
}
