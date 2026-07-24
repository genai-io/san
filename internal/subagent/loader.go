package subagent

import (
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/markdown"
	"go.uber.org/zap"
)

// LoadAgentSystemPrompt loads just the system prompt body from an Agent file.
func LoadAgentSystemPrompt(filePath string) string {
	_, body, err := markdown.ParseFrontmatterFile(filePath)
	if err != nil {
		log.Logger().Debug("Failed to read agent file for system prompt",
			zap.String("path", filePath),
			zap.Error(err))
		return ""
	}
	return body
}
