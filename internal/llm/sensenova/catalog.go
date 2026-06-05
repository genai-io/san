package sensenova

import "github.com/genai-io/san/internal/llm"

var catalog = []llm.ModelInfo{
	{
		ID:               "sensenova-6.7-flash-lite",
		Name:             "SenseNova 6.7 Flash Lite",
		DisplayName:      "SenseNova 6.7 Flash Lite",
		InputTokenLimit:  128000,
		OutputTokenLimit: 65536,
	},
}

func StaticModels() []llm.ModelInfo {
	models := make([]llm.ModelInfo, len(catalog))
	copy(models, catalog)
	return models
}

func CatalogModel(modelID string) (llm.ModelInfo, bool) {
	for _, entry := range catalog {
		if entry.ID == modelID {
			return entry, true
		}
	}
	return llm.ModelInfo{}, false
}
