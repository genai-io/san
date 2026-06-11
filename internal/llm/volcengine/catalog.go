package volcengine

import (
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

func StaticModels() []llm.ModelInfo {
	modelID := secret.Resolve("VOLCENGINE_MODEL")
	if modelID == "" {
		return nil
	}
	return []llm.ModelInfo{{
		ID:          modelID,
		Name:        modelID,
		DisplayName: modelID,
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
