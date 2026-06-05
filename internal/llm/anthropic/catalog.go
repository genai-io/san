package anthropic

import (
	"strings"

	"github.com/genai-io/san/v2/internal/llm"
)

const (
	ThinkingOff    = "off"
	ThinkingNormal = "think"
	ThinkingHigh   = "think+"
	ThinkingUltra  = "ultrathink"
)

var thinkingEfforts = []string{ThinkingOff, ThinkingNormal, ThinkingHigh, ThinkingUltra}

type catalogEntry struct {
	match            func(string) bool
	info             llm.ModelInfo
	supportsThinking bool
}

var anthropicCatalog = []catalogEntry{
	{
		match:            matchAnyPrefix("claude-opus-4-7"),
		info:             newModelInfo("claude-opus-4-7", "Claude Opus 4.7", "Claude Opus 4.7 (Most Capable)", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-opus-4-6[1m]"),
		info:             newModelInfo("claude-opus-4-6[1m]", "Claude Opus 4.6 (1M)", "Claude Opus 4.6 (1M Context)", 1000000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-opus-4-6"),
		info:             newModelInfo("claude-opus-4-6", "Claude Opus 4.6", "Claude Opus 4.6", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-opus-4-5"),
		info:             newModelInfo("claude-opus-4-5-20251101", "Claude Opus 4.5", "Claude Opus 4.5", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-opus-4-1"),
		info:             newModelInfo("claude-opus-4-1-20250805", "Claude Opus 4.1", "Claude Opus 4.1", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-opus-4"),
		info:             newModelInfo("claude-opus-4-20250514", "Claude Opus 4", "Claude Opus 4", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-sonnet-4-6"),
		info:             newModelInfo("claude-sonnet-4-6", "Claude Sonnet 4.6", "Claude Sonnet 4.6", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-sonnet-4-5"),
		info:             newModelInfo("claude-sonnet-4-5-20250929", "Claude Sonnet 4.5", "Claude Sonnet 4.5", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-sonnet-4"),
		info:             newModelInfo("claude-sonnet-4-20250514", "Claude Sonnet 4", "Claude Sonnet 4", 200000),
		supportsThinking: true,
	},
	{
		match:            matchAnyPrefix("claude-3-7-sonnet"),
		info:             newModelInfo("claude-3-7-sonnet-20250219", "Claude Sonnet 3.7", "Claude Sonnet 3.7", 200000),
		supportsThinking: true,
	},
	{
		match: matchAnyPrefix("claude-haiku-4-5"),
		info:  newModelInfo("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "Claude Haiku 4.5 (Fast)", 200000),
	},
	{
		match: matchAnyPrefix("claude-3-5-haiku"),
		info:  newModelInfo("claude-3-5-haiku-20241022", "Claude Haiku 3.5", "Claude Haiku 3.5", 200000),
	},
}

func (c *Client) ThinkingEfforts(model string) []string {
	if !supportsThinkingModel(model) {
		return nil
	}
	return thinkingEfforts
}

func (c *Client) DefaultThinkingEffort(model string) string {
	if !supportsThinkingModel(model) {
		return ""
	}
	return ThinkingOff
}

func CatalogModel(modelID string) (llm.ModelInfo, bool) {
	normalized := normalizeModelID(modelID)
	if normalized == "" {
		return llm.ModelInfo{}, false
	}
	for _, entry := range anthropicCatalog {
		if entry.match(normalized) {
			info := entry.info
			info.ID = modelID
			info.Name = entry.info.Name
			info.DisplayName = entry.info.DisplayName
			return info, true
		}
	}
	return llm.ModelInfo{}, false
}

func supportsThinkingModel(modelID string) bool {
	normalized := normalizeModelID(modelID)
	if normalized == "" {
		return false
	}
	for _, entry := range anthropicCatalog {
		if entry.match(normalized) {
			return entry.supportsThinking
		}
	}
	return false
}

func StaticModels() []llm.ModelInfo {
	models := make([]llm.ModelInfo, 0, len(anthropicCatalog))
	for _, entry := range anthropicCatalog {
		models = append(models, entry.info)
	}
	return models
}

func newModelInfo(id, name, displayName string, inputLimit int) llm.ModelInfo {
	return llm.ModelInfo{
		ID:              id,
		Name:            name,
		DisplayName:     displayName,
		InputTokenLimit: inputLimit,
	}
}

func matchAnyPrefix(prefix string) func(string) bool {
	return func(model string) bool {
		return strings.HasPrefix(model, prefix)
	}
}

func normalizeModelID(modelID string) string {
	return strings.ToLower(strings.TrimSpace(modelID))
}
