package deepseek

import "github.com/genai-io/gen-code/internal/llm"

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

var catalog = []modelCatalogEntry{
	{
		info: llm.ModelInfo{
			ID:               "deepseek-chat",
			Name:             "DeepSeek V3",
			DisplayName:      "DeepSeek V3",
			InputTokenLimit:  128000,
			OutputTokenLimit: 8192,
		},
		pricing: pricing{inputPerMTokens: 0.27, outputPerMTokens: 1.10, cacheReadPerMTokens: 0.07, cacheWritePerMTokens: 0.27},
	},
	{
		info: llm.ModelInfo{
			ID:               "deepseek-reasoner",
			Name:             "DeepSeek R1",
			DisplayName:      "DeepSeek R1",
			InputTokenLimit:  128000,
			OutputTokenLimit: 8192,
		},
		pricing: pricing{inputPerMTokens: 0.55, outputPerMTokens: 2.19, cacheReadPerMTokens: 0.14, cacheWritePerMTokens: 0.55},
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

func EstimateCost(modelID string, usage llm.Usage) (llm.Money, bool) {
	for _, entry := range catalog {
		if entry.info.ID != modelID {
			continue
		}
		const perMillion = 1_000_000.0
		cost := (float64(usage.InputTokens) / perMillion) * entry.pricing.inputPerMTokens
		cost += (float64(usage.OutputTokens) / perMillion) * entry.pricing.outputPerMTokens
		cost += (float64(usage.CacheReadInputTokens) / perMillion) * entry.pricing.cacheReadPerMTokens
		cost += (float64(usage.CacheCreationInputTokens) / perMillion) * entry.pricing.cacheWritePerMTokens
		return llm.Money{Amount: cost, Currency: llm.CurrencyUSD}, true
	}
	return llm.Money{}, false
}
