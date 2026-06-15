package volcengine

import (
	"strings"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

func StaticModels() []llm.ModelInfo {
	modelID := secret.Resolve("VOLCENGINE_MODEL")
	if modelID == "" {
		return nil
	}
	input, output := staticLimits(modelID)
	return []llm.ModelInfo{{
		ID:               modelID,
		Name:             modelID,
		DisplayName:      modelID,
		InputTokenLimit:  input,
		OutputTokenLimit: output,
	}}
}

func CatalogModel(modelID string) (llm.ModelInfo, bool) {
	for _, entry := range StaticModels() {
		if entry.ID == modelID {
			return entry, true
		}
	}
	return llm.ModelInfo{}, false
}

// staticLimits returns known context/output windows for Volcengine Doubao
// model IDs. Doubao model IDs embed the context length as a suffix
// (e.g. "doubao-pro-256k"); fall back to 256K for seed/latest variants.
func staticLimits(modelID string) (input, output int) {
	m := strings.ToLower(modelID)
	switch {
	case strings.Contains(m, "256k"):
		return 256_000, 8_000
	case strings.Contains(m, "128k"):
		return 128_000, 8_000
	case strings.Contains(m, "32k"):
		return 32_000, 4_000
	case strings.Contains(m, "seed"), strings.Contains(m, "1.6"):
		return 256_000, 8_000
	}
	return 0, 0
}
