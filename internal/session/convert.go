package session

import (
	"github.com/genai-io/san/v2/internal/core"
)

func ConvertToEntries(messages []core.ChatMessage) []Entry {
	entries := make([]Entry, 0, len(messages))
	var prevUUID string

	for _, msg := range messages {
		if msg.Role == core.RoleNotice {
			continue
		}

		// Prefer the stable ID stamped at conv.Append time; fall back to a
		// fresh one only for messages that arrive without an ID (legacy
		// paths, tool results assembled inline, etc.). Stable IDs are
		// required for the append-only save path's dedup to work.
		uuid := msg.ID
		if uuid == "" {
			uuid = generateShortID()
		}

		var parentUuid *string
		if prevUUID != "" {
			s := prevUUID
			parentUuid = &s
		}

		entry := Entry{
			UUID:       uuid,
			ParentUuid: parentUuid,
			Version:    GetAppVersion(),
		}

		switch msg.Role {
		case core.RoleUser:
			entry.Type = EntryUser
			if msg.ToolResult != nil {
				entry.Message = &EntryMessage{
					Role:    "user",
					Content: toolResultToBlocks(msg.ToolResult),
				}
			} else {
				entry.Message = &EntryMessage{
					Role:    "user",
					Content: userContentToBlocks(msg.Content, msg.DisplayContent, msg.Images),
				}
			}

		case core.RoleAssistant:
			entry.Type = EntryAssistant
			entry.Message = &EntryMessage{
				Role:    "assistant",
				Content: assistantContentToBlocks(msg.Content, msg.Thinking, msg.ThinkingSignature, msg.ToolCalls),
			}

		case core.RoleTool:
			entry.Type = EntryUser
			if msg.ToolResult != nil {
				entry.Message = &EntryMessage{
					Role:    "user",
					Content: toolResultToBlocks(msg.ToolResult),
				}
			}

		default:
			continue
		}

		entries = append(entries, entry)
		prevUUID = uuid
	}

	return entries
}

func ConvertFromEntries(entries []Entry) []core.ChatMessage {
	coreMsgs := EntriesToMessages(entries)
	messages := make([]core.ChatMessage, 0, len(coreMsgs))

	for _, m := range coreMsgs {
		chatMsg := core.ChatMessage{
			Role:              m.Role,
			Content:           m.Content,
			DisplayContent:    m.DisplayContent,
			Images:            m.Images,
			Thinking:          m.Thinking,
			ThinkingSignature: m.ThinkingSignature,
			ToolCalls:         m.ToolCalls,
		}
		if m.ToolResult != nil {
			chatMsg.ToolResult = m.ToolResult
			chatMsg.ToolName = m.ToolResult.ToolName
		}
		messages = append(messages, chatMsg)
	}

	return messages
}
