package hub

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/genai-io/san/v2/internal/task"
)

// Message is a notification delivered to an agent.
// Notice is displayed in the TUI; Content is sent to the LLM.
type Message struct {
	Notice  string
	Content string
}

// TaskMessage builds a Message from a completed task.
// Returns (Message{}, false) if the task status is not terminal.
func TaskMessage(info task.TaskInfo, subject string) (Message, bool) {
	status := formatStatus(info.Status)
	if status == "" {
		return Message{}, false
	}

	description := subject
	if description == "" {
		description = "Background task"
	}

	result := strings.TrimSpace(info.Output)
	if len(result) > 4000 {
		result = strings.TrimSpace(result[:4000]) + "\n...[truncated]"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<task-notification task-id=%q status=%q", info.ID, status)
	if info.AgentSessionID != "" {
		fmt.Fprintf(&b, " agent-id=%q", info.AgentSessionID)
	}
	if description != "" {
		fmt.Fprintf(&b, " description=%q", description)
	}
	if info.OutputFile != "" {
		fmt.Fprintf(&b, " output-file=%q", info.OutputFile)
	}
	b.WriteString(">\n")
	if info.Error != "" {
		b.WriteString(escapeXMLText(info.Error))
	} else if result != "" {
		b.WriteString(escapeXMLText(result))
	}
	b.WriteString("\n</task-notification>")

	return Message{
		Notice:  fmt.Sprintf("%s %s", description, status),
		Content: b.String(),
	}, true
}

// Merge combines multiple messages into one.
func Merge(items []Message) Message {
	if len(items) == 0 {
		return Message{}
	}
	if len(items) == 1 {
		return items[0]
	}

	notices := make([]string, 0, min(len(items), 3))
	for _, item := range items[:min(len(items), 3)] {
		if item.Notice != "" {
			notices = append(notices, item.Notice)
		}
	}
	notice := fmt.Sprintf("%d background tasks completed", len(items))
	if len(notices) > 0 {
		notice += ": " + strings.Join(notices, "; ")
		if len(items) > len(notices) {
			notice += "; ..."
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<task-notifications count=\"%d\">\n", len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content != "" {
			sb.WriteString(content)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("</task-notifications>")

	return Message{
		Notice:  notice,
		Content: sb.String(),
	}
}

// TaskSubject generates a human-readable subject line from task info.
func TaskSubject(info task.TaskInfo) string {
	switch info.Type {
	case task.TaskTypeAgent:
		if s := joinNameDesc(info.AgentName, info.Description); s != "" {
			return s
		}
	case task.TaskTypeBash:
		if info.Command != "" {
			return info.Command
		}
	}
	return info.Description
}

func formatStatus(status task.TaskStatus) string {
	switch status {
	case task.StatusCompleted:
		return "completed"
	case task.StatusFailed:
		return "failed"
	case task.StatusKilled:
		return "killed"
	default:
		return ""
	}
}

func escapeXMLText(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

func joinNameDesc(name, desc string) string {
	name = strings.TrimSpace(name)
	desc = strings.TrimSpace(desc)
	switch {
	case name != "" && desc != "" && !strings.EqualFold(name, desc):
		return name + ": " + desc
	case desc != "":
		return desc
	case name != "":
		return name
	default:
		return ""
	}
}
