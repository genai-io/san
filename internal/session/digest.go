package session

import (
	"github.com/genai-io/san/internal/core"
)

// messageIDs is the active chain in send order — what the next inference
// referenced, so replay can check it referenced what the transcript holds.
func messageIDs(msgs []core.Message) []string {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}
