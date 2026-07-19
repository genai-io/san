package moonshot

import "strings"

// staticInputLimit returns the known context window for Kimi/Moonshot model
// IDs when the /v1/models response does not include context_length.
func staticInputLimit(modelID string) int {
	m := strings.ToLower(modelID)
	switch {
	case strings.Contains(m, "128k"):
		return 131_072
	case strings.Contains(m, "32k"):
		return 32_768
	case strings.Contains(m, "8k"):
		return 8_192
	case strings.Contains(m, "k2"):
		return 262_144
	}
	return 0
}

// staticOutputLimit returns the known max output tokens for Kimi/Moonshot
// model IDs. Used as a fallback when the API doesn't include
// max_output_tokens in the model metadata.
func staticOutputLimit(modelID string) int {
	m := strings.ToLower(modelID)
	switch {
	case strings.Contains(m, "k2"):
		return 8_192
	case strings.Contains(m, "128k"):
		return 8_192
	case strings.Contains(m, "32k"):
		return 8_192
	case strings.Contains(m, "8k"):
		return 3_000
	}
	return 0
}
