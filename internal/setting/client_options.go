package setting

// The input-token counterpart of DefaultMaxTokens lives in the llm package
// (llm.DefaultInputLimit), next to the resolver that applies it.
const (
	DefaultMaxTokens    = 8192
	DefaultSystemPrompt = "You are a helpful AI coding assistant."
)

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
