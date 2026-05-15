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
			ID:               "deepseek-v4-flash",
			Name:             "DeepSeek V4 Flash",
			DisplayName:      "DeepSeek V4 Flash",
			InputTokenLimit:  1_000_000,
			OutputTokenLimit: 384000,
		},
		pricing: pricing{inputPerMTokens: 0.14, outputPerMTokens: 0.28, cacheReadPerMTokens: 0.028, cacheWritePerMTokens: 0.14},
	},
	{
		info: llm.ModelInfo{
			ID:               "deepseek-v4-pro",
			Name:             "DeepSeek V4 Pro",
			DisplayName:      "DeepSeek V4 Pro",
			InputTokenLimit:  1_000_000,
			OutputTokenLimit: 384000,
		},
		pricing: pricing{inputPerMTokens: 1.74, outputPerMTokens: 3.48, cacheReadPerMTokens: 0.145, cacheWritePerMTokens: 1.74},
	},
	// Legacy names — mapped by DeepSeek API to deepseek-v4-flash (deprecated July 24, 2026)
	{
		info: llm.ModelInfo{
			ID:               "deepseek-chat",
			Name:             "DeepSeek Chat (legacy → V4 Flash)",
			DisplayName:      "DeepSeek Chat",
			InputTokenLimit:  1_000_000,
			OutputTokenLimit: 384000,
		},
		pricing: pricing{inputPerMTokens: 0.14, outputPerMTokens: 0.28, cacheReadPerMTokens: 0.028, cacheWritePerMTokens: 0.14},
	},
	{
		info: llm.ModelInfo{
			ID:               "deepseek-reasoner",
			Name:             "DeepSeek Reasoner (legacy → V4 Flash thinking)",
			DisplayName:      "DeepSeek Reasoner",
			InputTokenLimit:  1_000_000,
			OutputTokenLimit: 384000,
		},
		pricing: pricing{inputPerMTokens: 0.14, outputPerMTokens: 0.28, cacheReadPerMTokens: 0.028, cacheWritePerMTokens: 0.14},
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
