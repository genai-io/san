// Main-loop notifications: the small events that wake the TUI's Update loop
// between turns — a finished background subagent, an interim message from a
// running one, a self-learn review tick. They arrive on m.mainNotices and are
// drained at turn boundaries (see model_turn_queue.go).
//
// m.mainNotices is a TUI staging channel, not the main agent's inbox: unlike a
// subagent (whose broker delivery goes straight into its core.Agent inbox),
// the main conversation is UI-attached, so a message must first surface as a
// notice and wait for a turn boundary before SubmitToAgent forwards it into
// the main agent's real inbox. That extra hop is why this channel carries
// mainNotice (a notice) rather than a raw message.
package app

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
)

// mainNotice is one message routed to the main conversation on Source 2 — a
// subagent completion or an interim message. It carries data only, no control
// flags: Display is the one-line notice shown to the user (may be empty);
// Content, if non-empty, is submitted to the main agent as a fresh turn (empty
// Content = display-only). Pure UI signals (e.g. the self-learn spinner start)
// do not ride here — they have their own channels.
type mainNotice struct {
	Display string
	Content string
}

// fromBrokerMessage converts a message the broker routed to "main" (a subagent
// completion or an interim message) into a main-loop notice.
func fromBrokerMessage(m broker.Message) mainNotice {
	return mainNotice{Display: m.Subject, Content: m.Content}
}

// mergeNotices collapses notices drained together into one displayed line + one
// injected content. Display-only notices contribute their line but no content.
func mergeNotices(notices []mainNotice) mainNotice {
	if len(notices) == 1 {
		return notices[0]
	}
	lines := make([]string, 0, len(notices))
	contents := make([]string, 0, len(notices))
	for _, n := range notices {
		if n.Display != "" {
			lines = append(lines, n.Display)
		}
		if strings.TrimSpace(n.Content) != "" {
			contents = append(contents, strings.TrimSpace(n.Content))
		}
	}
	merged := mainNotice{Display: strings.Join(lines, "; ")}
	if len(contents) == 1 {
		merged.Content = contents[0]
	} else if len(contents) > 1 {
		merged.Content = "<agent-messages>\n" + strings.Join(contents, "\n") + "\n</agent-messages>"
	}
	return merged
}

// maxTaskOutputInNotification caps how much of a task's output rides in the
// completion notification; the full transcript stays in the output file.
const maxTaskOutputInNotification = 4000

// taskCompletionMessage builds the broker message a finished task sends to the
// "main" address, or (_, false) if the task is not in a terminal state.
func taskCompletionMessage(info task.TaskInfo) (broker.Message, bool) {
	status := formatStatus(info.Status)
	if status == "" {
		return broker.Message{}, false
	}

	description := taskSubject(info)
	if description == "" {
		description = "Background task"
	}

	result := strings.TrimSpace(info.Output)
	if len(result) > maxTaskOutputInNotification {
		result = strings.TrimSpace(result[:maxTaskOutputInNotification]) + "\n...[truncated]"
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

	return broker.Message{
		From:    info.ID,
		To:      broker.Main,
		Subject: fmt.Sprintf("%s %s", description, status),
		Content: b.String(),
	}, true
}

// taskSubject generates a human-readable subject line from task info.
func taskSubject(info task.TaskInfo) string {
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
	case task.StatusStopped:
		return "stopped"
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
