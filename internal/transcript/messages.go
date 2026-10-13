package transcript

import (
	"fmt"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// A message ID names immutable content. Replacements select an ordered chain;
// editing a message requires a new ID, as does appending it to another branch.
func validateMessages(messages []ai.Message) error {
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if message.ID == "" {
			return fmt.Errorf("replacement message ID is required")
		}
		if _, exists := seen[message.ID]; exists {
			return fmt.Errorf("duplicate replacement message ID %q", message.ID)
		}
		seen[message.ID] = struct{}{}
	}
	return nil
}

func sameMessageIDs(ids []string, messages []ai.Message) bool {
	if len(ids) != len(messages) {
		return false
	}
	for i := range ids {
		if ids[i] != messages[i].ID {
			return false
		}
	}
	return true
}
