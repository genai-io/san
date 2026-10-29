package agent

import (
	"fmt"

	"github.com/genai-io/san/internal/tool"
)

// The parent needs findings and inspection pointers; usage and activity travel
// separately in Details, so the UI never has to parse the worker's prose.
func formatForegroundAgentResult(result *tool.AgentExecResult) string {
	output := result.Content
	if result.Identity.ID != "" {
		output += fmt.Sprintf("\n\nTask ID: %s", result.Identity.ID)
	}
	if result.AgentID != "" {
		output += fmt.Sprintf("\n\nAgentID: %s", result.AgentID)
	}
	if result.OutputFile != "" {
		output += "\n\nTranscript: " + result.OutputFile
	}
	return output
}
