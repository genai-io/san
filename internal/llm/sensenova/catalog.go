package sensenova

import "github.com/genai-io/san/internal/llm"

var catalog = []llm.ModelInfo{
	{
		ID:               "sensenova-6.7-flash-lite",
		Name:             "SenseNova 6.7 Flash Lite",
		DisplayName:      "SenseNova 6.7 Flash Lite",
		InputTokenLimit:  256000,
		OutputTokenLimit: 65536,
	},
	{
		ID:               "deepseek-v4-flash",
		Name:             "DeepSeek V4 Flash (via SenseNova)",
		DisplayName:      "DeepSeek V4 Flash",
		InputTokenLimit:  1_000_000,
		OutputTokenLimit: 384000,
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
