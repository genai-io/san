package session

import (
	"regexp"
	"slices"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/sdk-go/pkg/ai"
)

const commandEnvelopePattern = `<command-name>.*?</command-name>\s*(?:<skill-invocation\b.*?</skill-invocation>|<custom-command\b.*?</custom-command>)\n*`

// harnessInjectedRe matches any harness-injected span. The command envelope only
// ever leads the message, so it's anchored; reminders can appear anywhere.
var harnessInjectedRe = regexp.MustCompile(`(?s)` + reminder.Pattern + `|<group-message>.*?</group-message>|^` + commandEnvelopePattern)

func ExtractLastUserText(msgs []core.Message) string {
	for _, msg := range slices.Backward(msgs) {
		if text, ok := extractUserText(msg); ok {
			return text
		}
	}
	return ""
}
func extractUserText(msg core.Message) (string, bool) {
	if msg.Role != ai.RoleUser || len(msg.ToolResults()) > 0 {
		return "", false
	}
	text := harnessInjectedRe.ReplaceAllString(msg.Text(), "")
	return text, text != ""
}
