package session

import (
	"github.com/genai-io/san/internal/core"
)

// ConvertToEntries turns the conversation view-model into transcript entries.
// Notices are display-only and dropped; the rest convert to wire Messages and
// share messagesToEntries with every other writer, so the stable IDs stamped at
// conv.Append time survive (the append-only save path dedupes by them).
func ConvertToEntries(messages []core.ChatMessage) []Entry {
	msgs := make([]core.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == core.RoleNotice {
			continue
		}
		msgs = append(msgs, msg.ToMessage())
	}
	return messagesToEntries(msgs)
}

func ConvertFromEntries(entries []Entry) []core.ChatMessage {
	coreMsgs := EntriesToMessages(entries)
	messages := make([]core.ChatMessage, 0, len(coreMsgs))
	for _, m := range coreMsgs {
		messages = append(messages, m.ToChat())
	}
	return messages
}
