package agnesai

import "strings"

// staticInputLimit returns the known context window for an Agnes-AI model ID.
// Used as a fallback when the /v1/models endpoint omits context_length.
//
//	agnes-2.0-flash: 256K tokens (per Agnes-AI's agnes-20-flash256 docs)
//	agnes-1.5-flash: 128K tokens (conservative default; not separately documented)
func staticInputLimit(modelID string) int {
	m := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.HasPrefix(m, "agnes-2.0"):
		return 256_000
	case strings.HasPrefix(m, "agnes-1.5"):
		return 128_000
	}
	return 0
}
