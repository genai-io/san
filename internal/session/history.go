package session

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// History is the selected native conversation, application state, and display
// information read from a transcript. It owns no runtime or writer resources.
type History struct {
	Metadata    Metadata
	Messages    []ai.Message
	Tasks       []todo.Item
	Notices     []transcript.NoticeRecord
	ToolDetails map[string]transcript.ToolDetailsRecord
}

// ChatRows projects the conversation for display. It is never a persistence source.
func ChatRows(messages []core.Message) []core.ChatMessage {
	var rows []core.ChatMessage
	for _, msg := range messages {
		for _, row := range core.ChatRowsOf(msg) {
			if row.Role == core.ChatUser && row.ToolResult == nil {
				var display strings.Builder
				image := 0
				for _, block := range msg.Content {
					switch block.Type {
					case ai.BlockText:
						display.WriteString(harnessInjectedRe.ReplaceAllString(block.Text, ""))
					case ai.BlockImage:
						image++
						fmt.Fprintf(&display, "[Image #%d]", image)
					}
				}
				row.DisplayContent = strings.TrimRight(display.String(), "\n")
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// ChatRows builds the display from native messages and separate presentation events.
func (s *History) ChatRows() []core.ChatMessage {
	rows := ChatRows(s.Messages)
	for i := range rows {
		if rows[i].ToolResult == nil {
			continue
		}
		details := s.ToolDetails[rows[i].ToolResult.ToolCallID]
		switch {
		case details.FileChange != nil:
			rows[i].ToolDetails = *details.FileChange
		case details.Bash != nil:
			rows[i].ToolDetails = *details.Bash
		case details.Agent != nil:
			rows[i].ToolDetails = *details.Agent
		}
	}
	return withNotices(rows, s.Notices)
}

func withNotices(rows []core.ChatMessage, notices []transcript.NoticeRecord) []core.ChatMessage {
	if len(notices) == 0 {
		return rows
	}
	after := make(map[string][]core.ChatMessage)
	for _, n := range notices {
		after[n.AfterMessageID] = append(after[n.AfterMessageID], core.ChatMessage{Role: core.ChatNotice, Content: n.Text, AgentNotice: n.Agent})
	}
	out := make([]core.ChatMessage, 0, len(rows)+len(notices))
	out = append(out, after[""]...)
	for i, row := range rows {
		out = append(out, row)
		if i+1 < len(rows) && rows[i+1].ID == row.ID {
			continue
		}
		out = append(out, after[row.ID]...)
	}
	return out
}

// ChatRows reads attachments for display without changing the native conversation.
func (s *Store) ChatRows(history *History) []core.ChatMessage {
	rows := history.ChatRows()
	if transcript.ValidateStorageID(history.Metadata.ID) != nil {
		return rows
	}
	marker := "\n\n[Full output persisted to blobs/tool-result/" + history.Metadata.ID + "/"
	for i := range rows {
		result := rows[i].ToolResult
		if result == nil {
			continue
		}
		result.Content = slices.Clone(result.Content)
		for j := range result.Content {
			block := &result.Content[j]
			if block.Type != ai.BlockText {
				continue
			}
			_, after, ok := strings.Cut(block.Text, marker)
			if !ok {
				continue
			}
			id, _, ok := strings.Cut(after, "]")
			if !ok || transcript.ValidateStorageID(id) != nil {
				continue
			}
			if data, err := os.ReadFile(filepath.Join(s.toolResultsDir(history.Metadata.ID), id)); err == nil {
				block.Text = string(data)
			}
		}
	}
	return rows
}
