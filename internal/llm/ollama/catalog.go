package ollama

import "github.com/genai-io/gen-code/internal/llm"

// pricing is not applicable for locally-hosted Ollama models.
type pricing struct {
	inputPerMTokens      float64
	outputPerMTokens     float64
	cacheReadPerMTokens  float64
	cacheWritePerMTokens float64
}

type modelCatalogEntry struct {
	info    llm.ModelInfo
	pricing pricing
}

// catalog maps well-known Ollama model IDs to display names.
// Dynamic models (user-pulled) are returned directly from the API.
var catalog = []modelCatalogEntry{
	{
		info: llm.ModelInfo{
			ID:               "llama4",
			Name:             "Llama 4",
			DisplayName:      "Llama 4",
			InputTokenLimit:  131072,
			OutputTokenLimit: 16384,
		},
		pricing: pricing{},
	},
	{
		info: llm.ModelInfo{
			ID:               "qwq",
			Name:             "QwQ",
			DisplayName:      "QwQ",
			InputTokenLimit:  131072,
			OutputTokenLimit: 16384,
		},
		pricing: pricing{},
	},
	{
		info: llm.ModelInfo{
			ID:               "gemma3",
			Name:             "Gemma 3",
			DisplayName:      "Gemma 3",
			InputTokenLimit:  131072,
			OutputTokenLimit: 16384,
		},
		pricing: pricing{},
	},
	{
		info: llm.ModelInfo{
			ID:               "mistral",
			Name:             "Mistral",
			DisplayName:      "Mistral",
			InputTokenLimit:  131072,
			OutputTokenLimit: 16384,
		},
		pricing: pricing{},
	},
}

func StaticModels() []llm.ModelInfo {
	models := make([]llm.ModelInfo, len(catalog))
	for i, entry := range catalog {
		models[i] = entry.info
	}
	return models
}

func CatalogModel(modelID string) (llm.ModelInfo, bool) {
	for _, entry := range catalog {
		if entry.info.ID == modelID {
			return entry.info, true
		}
	}
	return llm.ModelInfo{}, false
}

// EstimateCost always returns zero cost for Ollama — it's a local service.
func EstimateCost(modelID string, usage llm.Usage) (llm.Money, bool) {
	return llm.Money{Amount: 0, Currency: llm.CurrencyUSD}, true
}
