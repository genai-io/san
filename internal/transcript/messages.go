package transcript

import (
	"fmt"
	"slices"

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

func sameMessageIDs(a, b []ai.Message) bool {
	return slices.EqualFunc(a, b, func(x, y ai.Message) bool { return x.ID == y.ID })
}
