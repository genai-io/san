package subagent

import (
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestSendMessageActivityShowsRecipientAndSubject(t *testing.T) {
	for _, tc := range []struct {
		to, subject string
		limit       int
		want        string
	}{
		{"main", "Publication agent\nready", 0, "To main: Publication agent ready"},
		{" @api ", "  ", 0, "To @api"},
		{"main", "发布准备完毕", 12, "To main: 发布…"},
		{"", "Missing recipient", 0, "SendMessage"},
	} {
		got := formatToolActivityLimit(nil, tool.ToolSendMessage, map[string]any{
			"to": tc.to, "subject": tc.subject, "message": "Full message body must stay out of the activity preview.",
		}, tc.limit)
		if got != tc.want {
			t.Errorf("to %q subject %q: got %q, want %q", tc.to, tc.subject, got, tc.want)
		}
	}
}
