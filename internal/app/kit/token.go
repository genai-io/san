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

// GetModelTokenLimits returns the cached token limits for the current model.
//
// It resolves from the current model's own provider/auth cache first. The same
// model ID can be cached under several providers with different context windows
// (e.g. gpt-5.5 via Direct API at 400k and via the ChatGPT subscription at
// 272k); a bare ID scan iterates a map and would return a random one each
// render, making the status-bar limit flicker. Resolving by the connected
// provider is deterministic and correct.
//
// It falls back to a cross-provider ID scan when the current provider's cache
// doesn't report a window — an OpenAI-compatible aggregator may serve a model
// with no advertised context window while the same model's native provider
// knows the real one, letting the status bar borrow it instead of showing "--".
func GetModelTokenLimits(store *llm.Store, currentModel *llm.CurrentModelInfo) (inputLimit, outputLimit int) {
	if store == nil || currentModel == nil {
		return 0, 0
	}
	if models, ok := store.GetCachedModels(currentModel.Provider, currentModel.AuthMethod); ok {
		for _, m := range models {
			if m.ID == currentModel.ModelID && m.InputTokenLimit > 0 {
				return m.InputTokenLimit, m.OutputTokenLimit
			}
		}
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
