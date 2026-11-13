package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// ChatRows converts native messages for display, never for persistence.
func ChatRows(messages []core.Message) []core.ChatMessage {
	var rows []core.ChatMessage
	for _, msg := range messages {
		for _, row := range core.ChatRowsOf(msg) {
			if row.Role == core.ChatUser && row.ToolResult == nil {
				var display strings.Builder
				imageCount := 0
				for _, block := range msg.Content {
					if block.Type == ai.BlockImage {
						imageCount++
					}
				}
				image := 0
				for _, block := range msg.Content {
					switch block.Type {
					case ai.BlockText:
						if !imageSourceNote(block.Text, imageCount) {
							display.WriteString(harnessInjectedRe.ReplaceAllString(block.Text, ""))
						}
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

// imageSourceNote recognizes model-only source metadata, not user prose.
func imageSourceNote(text string, imageCount int) bool {
	if imageCount == 0 {
		return false
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == imageCount+1 && strings.HasPrefix(lines[0], "[Attached image(s) —") {
		lines = lines[1:]
	}
	if len(lines) != imageCount {
		return false
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, fmt.Sprintf("[Image #%d: ", i+1)) || !strings.HasSuffix(line, "]") {
			return false
		}
	}
	return true
}

// ChatRows renders full persisted history, including compacted messages and
// display-only facts. It does not acquire a writer or change the projection.
func (s *Store) ChatRows(projection Projection) []core.ChatMessage {
	rows := ChatRows(projection.Messages())
	after := make(map[string][]core.ChatMessage)
	details := make(map[string][]byte)
	last := ""
	for _, event := range projection.Events() {
		switch p := event.Payload.(type) {
		case *MessageAppendPayload:
			last = p.Messages[len(p.Messages)-1].ID
		case *ContextCompactPayload:
			last = p.Summary.ID
		case *AnnotationAddPayload:
			anchor := p.AfterMessage
			if anchor == "" {
				anchor = last
			}
			after[anchor] = append(after[anchor], core.ChatMessage{Role: core.ChatNotice, Content: p.Text, AgentNotice: p.ByAgent})
		case *ToolFinishPayload:
			details[p.CallID] = p.Details
		}
	}
	marker := "\n\n[Full output persisted to blobs/tool-result/"
	for i := range rows {
		result := rows[i].ToolResult
		if result == nil {
			continue
		}
		rows[i].ToolDetails = displayToolDetails(details[result.ToolCallID])
		if ValidateStorageID(projection.SessionID()) != nil {
			continue
		}
		for j := range result.Content {
			block := &result.Content[j]
			if block.Type != ai.BlockText {
				continue
			}
			_, suffix, ok := strings.Cut(block.Text, marker)
			if !ok {
				continue
			}
			// Forked messages keep their original marker; resolve only inside
			// this session, never by following the marker's session directory.
			origin, suffix, ok := strings.Cut(suffix, "/")
			if !ok || ValidateStorageID(origin) != nil {
				continue
			}
			id, _, ok := strings.Cut(suffix, "]")
			if !ok || ValidateStorageID(id) != nil {
				continue
			}
			if data, err := os.ReadFile(filepath.Join(s.toolResultsDir(projection.SessionID()), id)); err == nil {
				block.Text = string(data)
			}
		}
	}
	out := make([]core.ChatMessage, 0, len(rows))
	out = append(out, after[""]...)
	for i, row := range rows {
		out = append(out, row)
		if i+1 == len(rows) || rows[i+1].ID != row.ID {
			out = append(out, after[row.ID]...)
		}
	}
	return out
}

func (s *Store) toolResultsDir(id string) string {
	return filepath.Join(s.sessionDir(id), "blobs", "tool-result")
}

// ImagesDir holds pasted image files alongside the session's other assets.
// Invalid identifiers return no path.
func (s *Store) ImagesDir(id string) string {
	if ValidateStorageID(id) != nil {
		return ""
	}
	return filepath.Join(s.sessionDir(id), "blobs", "images")
}

// PersistToolResult saves the complete body referenced by a ResultPager preview.
func (s *Store) PersistToolResult(sessionID, callID, content string) error {
	if err := s.checkID(context.Background(), sessionID); err != nil {
		return err
	}
	if err := ValidateStorageID(callID); err != nil {
		return err
	}
	if _, err := os.Stat(s.eventsPath(sessionID)); err != nil {
		return err
	}
	dir := s.toolResultsDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, callID), []byte(content), 0o600)
}
