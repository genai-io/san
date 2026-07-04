package kit

import (
	"fmt"

	"github.com/genai-io/san/internal/llm"
)

// TokenLimitResultMsg is sent when a token limit fetch completes.
type TokenLimitResultMsg struct {
	Result string
	Err    error
}

// FormatTokenCount formats a token count for display.
func FormatTokenCount(count int) string {
	switch {
	case count >= 1000000:
		return fmt.Sprintf("%.1fM", float64(count)/1000000)
	case count >= 1000:
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	default:
		return fmt.Sprintf("%d", count)
	}
}

// GetMaxTokens returns the effective output limit, falling back to defaultMaxTokens.
func GetMaxTokens(store *llm.Store, currentModel *llm.CurrentModelInfo, defaultMaxTokens int) int {
	if limit := getEffectiveOutputLimit(store, currentModel); limit > 0 {
		return limit
	}
	return defaultMaxTokens
}

// GetModelTokenLimits returns the cached context window for the current model.
//
// The same model ID can be cached under several provider/auth keys with
// different windows (gpt-5.5: 400k via Direct API, 272k via ChatGPT
// subscription). Scanning the cache map for the ID picks a random one each
// render — that is what made the status-bar limit flicker. So we resolve in two
// deterministic steps:
//
//  1. this model's own provider+auth cache — the correct window. Ignores the 24h
//     TTL, otherwise an expired cache would fall to step 2 and flicker again.
//  2. else the largest window for the ID across all caches — covers a model an
//     aggregator serves with no window while its native provider knows the real one.
func GetModelTokenLimits(store *llm.Store, currentModel *llm.CurrentModelInfo) (inputLimit, outputLimit int) {
	if store == nil || currentModel == nil {
		return 0, 0
	}
	authMethod := currentModel.AuthMethod
	if authMethod == "" {
		if conn, ok := store.GetConnection(currentModel.Provider); ok {
			authMethod = conn.AuthMethod
		}
	}
	if input, output := store.CachedModelLimitsForProvider(currentModel.Provider, authMethod, currentModel.ModelID); input > 0 {
		return input, output
	}
	return store.CachedModelLimits(currentModel.ModelID)
}

// getEffectiveTokenLimits returns custom limits if set, otherwise cached model limits.
func getEffectiveTokenLimits(store *llm.Store, currentModel *llm.CurrentModelInfo) (inputLimit, outputLimit int) {
	if currentModel == nil {
		return 0, 0
	}

	if store != nil {
		if input, output, ok := store.GetTokenLimit(currentModel.ModelID); ok {
			return input, output
		}
	}

	return GetModelTokenLimits(store, currentModel)
}

// GetEffectiveInputLimit returns only the effective input token limit.
func GetEffectiveInputLimit(store *llm.Store, currentModel *llm.CurrentModelInfo) int {
	input, _ := getEffectiveTokenLimits(store, currentModel)
	return input
}

// getEffectiveOutputLimit returns only the effective output token limit.
func getEffectiveOutputLimit(store *llm.Store, currentModel *llm.CurrentModelInfo) int {
	_, output := getEffectiveTokenLimits(store, currentModel)
	return output
}
