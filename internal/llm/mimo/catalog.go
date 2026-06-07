package mimo

import "github.com/genai-io/san/internal/llm"

type pricing struct {
	inputPerMTokens     float64
	outputPerMTokens    float64
	cacheReadPerMTokens float64
}

type modelCatalogEntry struct {
	info    llm.ModelInfo
	pricing pricing
}

var catalog = []modelCatalogEntry{
	{
		info: llm.ModelInfo{
			ID:               "xiaomi/mimo-v2.5-pro",
			Name:             "MiMo V2.5 Pro",
			DisplayName:      "MiMo V2.5 Pro",
			InputTokenLimit:  1048576,
			OutputTokenLimit: 131072,
		},
		pricing: pricing{inputPerMTokens: 0.435, outputPerMTokens: 0.87, cacheReadPerMTokens: 0.0036},
	},
	{
		info: llm.ModelInfo{
			ID:               "xiaomi/mimo-v2.5",
			Name:             "MiMo V2.5",
			DisplayName:      "MiMo V2.5 (Multimodal)",
			InputTokenLimit:  1048576,
			OutputTokenLimit: 131072,
		},
		pricing: pricing{inputPerMTokens: 0.14, outputPerMTokens: 0.28, cacheReadPerMTokens: 0.0028},
	},
	{
		info: llm.ModelInfo{
			ID:               "xiaomi/mimo-v2-pro",
			Name:             "MiMo V2 Pro",
			DisplayName:      "MiMo V2 Pro",
			InputTokenLimit:  1048576,
			OutputTokenLimit: 131072,
		},
		pricing: pricing{inputPerMTokens: 1.0, outputPerMTokens: 3.0, cacheReadPerMTokens: 0.2},
	},
	{
		info: llm.ModelInfo{
			ID:               "xiaomi/mimo-v2-flash",
			Name:             "MiMo V2 Flash",
			DisplayName:      "MiMo V2 Flash",
			InputTokenLimit:  262144,
			OutputTokenLimit: 65536,
		},
		pricing: pricing{inputPerMTokens: 0.1, outputPerMTokens: 0.3, cacheReadPerMTokens: 0.01},
	},
	{
		info: llm.ModelInfo{
			ID:               "xiaomi/mimo-v2-omni",
			Name:             "MiMo V2 Omni",
			DisplayName:      "MiMo V2 Omni (Multimodal)",
			InputTokenLimit:  262144,
			OutputTokenLimit: 65536,
		},
		pricing: pricing{inputPerMTokens: 0.4, outputPerMTokens: 2.0, cacheReadPerMTokens: 0.08},
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
		return llm.Money{Amount: cost, Currency: llm.CurrencyUSD}, true
	}
	return llm.Money{}, false
}
