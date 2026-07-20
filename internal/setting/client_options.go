package setting

import (
	"os"
	"strconv"
)

const (
	DefaultMaxTokens    = 8192
	DefaultSystemPrompt = "You are a helpful AI coding assistant."

	// DefaultInputLimit is the context window assumed when neither the user
	// nor the provider supplies one. Auto-compaction keys off this limit, so
	// leaving it at 0 disables compaction entirely and lets the conversation
	// grow until the provider rejects it — a conservative guess degrades far
	// better than no guess. 128k is the smallest window in common use among
	// current models, so it under-guesses on roomier ones (compacting earlier
	// than strictly necessary) rather than over-guessing and overflowing.
	DefaultInputLimit = 128_000
)

// InputLimitEnvVar overrides the context window for models whose real window
// is unknown to San. Set it when a provider under-reports (an aggregator that
// serves a 1M-token model without publishing limits) and DefaultInputLimit is
// leaving capacity unused.
const InputLimitEnvVar = "SAN_INPUT_LIMIT"

// InputLimitOverride returns the context window forced by InputLimitEnvVar, or
// 0 when it is unset or not a positive integer.
func InputLimitOverride() int {
	n, err := strconv.Atoi(os.Getenv(InputLimitEnvVar))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// DefaultModel returns the default model ID for a given provider and auth method.
func DefaultModel(providerName string, authMethod string) string {
	if providerName == "anthropic" && authMethod == "vertex" {
		return "claude-sonnet-4-5@20250929"
	}
	switch providerName {
	case "agnesai":
		return "agnes-2.0-flash"
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4o"
	case "google":
		return "gemini-2.0-flash"
	case "moonshot":
		return "moonshot-v1-auto"
	case "alibaba":
		return "qwen-plus"
	case "minmax":
		return "MiniMax-M2.7"
	case "bigmodel":
		return "glm-5.1"
	case "deepseek":
		return "deepseek-v4-flash"
	case "sensenova":
		return "sensenova-6.7-flash-lite"
	default:
		return "claude-sonnet-4-20250514"
	}
}
