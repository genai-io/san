package agent

import "github.com/genai-io/san/internal/core"

// PendingInput marks user input for memory recall.
type PendingInput struct {
	core.Message
	FromUser bool
}

// InputQueue holds unsent input. The caller serializes access and gates delivery.
type InputQueue []PendingInput

// Deliver sends FIFO, retaining rejected input. Retries rerun prepare and user recall.
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
