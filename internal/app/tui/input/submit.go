// Pure helpers exposed for the submit dispatch in app/update_submit.go.
package input

import "strings"

// IsExitRequest reports whether `raw` is a case-insensitive "exit" or "quit"
// shortcut, which quits the app instead of sending to the agent.
func IsExitRequest(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	return raw == "exit" || raw == "quit"
}
