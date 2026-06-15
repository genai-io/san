package bigmodel

import "strings"

// staticInputLimit returns the known context window for a GLM model ID.
// Used as a fallback when BigModel's /v1/models endpoint omits
// context_length (which it does in practice — the endpoint follows the
// bare OpenAI shape with only id/object/owned_by).
//
//	GLM-5.2:             1M tokens
//	GLM-5.1 and earlier: 256K tokens
func staticInputLimit(modelID string) int {
	if strings.HasPrefix(modelID, "glm-5.2") {
		return 1_000_000
	}
	return 256_000
}
