package aside

import (
	"strings"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// UserAuthored reports whether msg carries text the user wrote, excluding tool
// results and appended system reminders. Asides that persist a change from a
// turn require it, so assistant claims alone never become durable data.
func UserAuthored(msg ai.Message) bool {
	if msg.Role != ai.RoleUser || len(msg.ToolResults()) != 0 {
		return false
	}
	text, _, _ := strings.Cut(msg.Content.Text(), "<system-reminder")
	return strings.TrimSpace(text) != ""
}
