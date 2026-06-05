package kit

import (
	"fmt"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

// TokenLimitResultMsg is sent when a token limit fetch completes.
type TokenLimitResultMsg struct {
	Result string
	Error  error
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

// ShouldAutoCompact returns true when context usage is high enough to trigger
// automatic compaction.
func ShouldAutoCompact(p llm.Provider, messageCount, inputTokens int, store *llm.Store, currentModel *llm.CurrentModelInfo) bool {
	if p == nil || messageCount < 3 {
		return false
	}
	return core.NeedsCompaction(inputTokens, GetEffectiveInputLimit(store, currentModel))
}

// GetContextUsagePercent returns what percentage of the context window is used.
func GetContextUsagePercent(inputTokens int, store *llm.Store, currentModel *llm.CurrentModelInfo) float64 {
	inputLimit := GetEffectiveInputLimit(store, currentModel)
	if inputLimit == 0 || inputTokens == 0 {
		return 0
	}
	return float64(inputTokens) / float64(inputLimit) * 100
}

// GetMaxTokens returns the effective output limit, falling back to defaultMaxTokens.
func GetMaxTokens(store *llm.Store, currentModel *llm.CurrentModelInfo, defaultMaxTokens int) int {
	if limit := getEffectiveOutputLimit(store, currentModel); limit > 0 {
		return limit
	}
	return defaultMaxTokens
}

// GetModelTokenLimits returns the cached token limits for the current model.
func GetModelTokenLimits(store *llm.Store, currentModel *llm.CurrentModelInfo) (inputLimit, outputLimit int) {
	if store == nil || currentModel == nil {
		return 0, 0
	}

	models, ok := store.GetCachedModels(currentModel.Provider, currentModel.AuthMethod)
	if !ok {
		return 0, 0
	}

	for _, model := range models {
		if model.ID == currentModel.ModelID {
			return model.InputTokenLimit, model.OutputTokenLimit
		}
	}
	return 0, 0
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
