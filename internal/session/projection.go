package session

import (
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session/transcript"
)

func EntriesToNodes(entries []Entry, sessionID, defaultCwd string, createdAt time.Time, gitBranch string) []transcript.Node {
	nodes := make([]transcript.Node, 0, len(entries))
	var prevID string

	for i := range entries {
		entry := &entries[i]
		if entry.Message == nil {
			continue
		}
		if entry.UUID == "" {
			entry.UUID = core.NewMessageID()
		}
		parentID := derefString(entry.ParentUuid)
		if parentID == "" && prevID != "" {
			parentID = prevID
			entry.ParentUuid = stringPtr(parentID)
		}
		if entry.Cwd == "" {
			entry.Cwd = defaultCwd
		}
		if entry.SessionID == "" {
			entry.SessionID = sessionID
		}
		if entry.GitBranch == "" {
			entry.GitBranch = gitBranch
		}
		if entry.Type == "" {
			entry.Type = entryTypeForRole(entry.Message.Role)
		}
		if entry.Timestamp.IsZero() {
			entry.Timestamp = createdAt.Add(time.Duration(i+1) * time.Millisecond)
		}

		nodes = append(nodes, transcript.Node{
			ID:          entry.UUID,
			ParentID:    parentID,
			Role:        entry.Message.Role,
			Time:        entry.Timestamp,
			Cwd:         entry.Cwd,
			GitBranch:   entry.GitBranch,
			AgentID:     entry.AgentID,
			IsSidechain: entry.IsSidechain,
			Content:     entry.Message.Content,
		})
		prevID = entry.UUID
	}

	return nodes
}

func EntriesFromNodes(sessionID string, nodes []transcript.Node) []Entry {
	entries := make([]Entry, 0, len(nodes))
	for _, node := range nodes {
		parentID := node.ParentID
		entry := Entry{
			Type:        entryTypeForRole(node.Role),
			ParentUuid:  stringPtr(parentID),
			IsSidechain: node.IsSidechain,
			Cwd:         node.Cwd,
			SessionID:   sessionID,
			GitBranch:   node.GitBranch,
			AgentID:     node.AgentID,
			UUID:        node.ID,
			Timestamp:   node.Time,
			Message: &EntryMessage{
				Role:    node.Role,
				Content: node.Content,
			},
		}
		if parentID == "" {
			entry.ParentUuid = nil
		}
		entries = append(entries, entry)
	}
	return entries
}

func entryTypeForRole(role string) string {
	switch role {
	case "assistant":
		return EntryAssistant
	default:
		return EntryUser
	}
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
