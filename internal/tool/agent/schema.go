package agent

import (
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

// AgentTool describes itself with the available-agents directory embedded when
// one is supplied at build time.
var _ tool.AgentDirectoryAwareTool = (*AgentTool)(nil)

// Schema returns the model-facing tool definition for Agent, without an
// available-agents directory. GetToolSchemasWith injects the directory via
// SchemaWithAgentDirectory when one is available.
func (t *AgentTool) Schema() core.ToolSchema {
	return agentSchema("")
}

// SchemaWithAgentDirectory returns the Agent schema with the available-agents
// directory embedded in the description. It satisfies tool.AgentDirectoryAwareTool
// so the schema follows the live agent catalog on each rebuild. An empty
// directory yields the same result as Schema.
func (t *AgentTool) SchemaWithAgentDirectory(agentDirectory string) core.ToolSchema {
	return agentSchema(agentDirectory)
}

// agentSchema builds the Agent tool schema with the given agent-directory body
// embedded directly in the description. The directory is rendered before the
// usage notes so the LLM sees the available agent types right after the
// opening line. An empty directory yields a directory-less description that
// still mentions subagent_type — useful for subagent contexts where the
// directory is intentionally omitted to discourage recursive spawning.
func agentSchema(agentDirectory string) core.ToolSchema {
	agentDirectory = strings.TrimSpace(agentDirectory)

	var sb strings.Builder
	sb.WriteString("Launch a subagent for complex work that benefits from separate context or parallel execution.\n\n")
	if agentDirectory != "" {
		sb.WriteString(agentDirectory)
		sb.WriteString("\n\n")
	}
	sb.WriteString("When using the Agent tool, specify a subagent_type parameter to select which agent type to use. If omitted, the general-purpose agent is used.\n\n")
	sb.WriteString("Use direct tools instead for simple reads, narrow searches, or tasks that only need 1-2 tool calls.\n\n")
	sb.WriteString("Usage notes:\n")
	sb.WriteString("- Always include a short description (3-5 words) summarizing what the agent will do\n")
	sb.WriteString("- Launch multiple agents concurrently whenever possible; to do that, use a single message with multiple Agent calls\n")
	sb.WriteString("- Each agent has isolated context; summarize important results back to the user yourself\n")
	sb.WriteString("- Use foreground by default when you need the result before continuing\n")
	sb.WriteString("- Use run_in_background only for genuinely independent work; you will be notified when it completes\n")
	sb.WriteString("- A running background agent can be steered mid-run with SendMessage(to=<task id>, message); it reports back when done\n")
	sb.WriteString("- Provide concrete prompts with file paths, constraints, and whether code changes are expected")

	return core.ToolSchema{
		Name:        "Agent",
		Description: sb.String(),
		Parameters:  agentToolParameters,
	}
}

var agentToolParameters = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"prompt": map[string]any{
			"type":        "string",
			"description": "The task for the agent to perform",
		},
		"description": map[string]any{
			"type":        "string",
			"description": "A short (3-5 word) description of the task",
		},
		"subagent_type": map[string]any{
			"type":        "string",
			"description": "The type of specialized agent to use for this task",
		},
		"name": map[string]any{
			"type":        "string",
			"description": "Optional short display name, usually 1-2 words. If omitted, explore mode uses Explorer and edit mode uses Editor.",
		},
		"run_in_background": map[string]any{
			"type":        "boolean",
			"description": "Set to true to run this agent in the background. You will be notified when it completes.",
		},
		"model": map[string]any{
			"type":        "string",
			"description": "Optional model override: an alias (sonnet, opus, haiku), a model id on the current provider, or vendor/model (e.g. deepseek/deepseek-v4) to route to another connected provider. If omitted or unavailable, inherits from parent conversation.",
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps for the agent. Built-in agents default to 100 and lower values are raised to 100.",
		},
		"mode": map[string]any{
			"type":        "string",
			"description": "Permission mode for spawned agent: explore = read-only, edit = can modify files, default = agent config's mode.",
			"enum":        []string{"explore", "edit", "default"},
		},
	},
	"required": []string{"description", "prompt"},
}

// Schema returns the model-facing tool definition for SendMessage.
func (t *SendMessageTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: "SendMessage",
		Description: `Send a message to another agent, routed by the broker. The message lands in the recipient's inbox and is read at its next step (a running subagent) or turn boundary (the main conversation).

Recipients (to):
- a running subagent's task id — steer or add information to a subagent that is still working.
- "main" — from inside a subagent, send an interim note to the main conversation without ending your run.

Notes:
- Delivery is best-effort: a subagent that has finished (or never takes another step) will not see the message. A subagent's final result comes back on its own when it completes — do not use SendMessage for it.
- The recipient sees the message as a user turn — make it self-contained.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to": map[string]any{
					"type":        "string",
					"description": "Recipient address: a running subagent's task id, or \"main\".",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message to deliver. Self-contained — the recipient reads it as a user turn.",
				},
			},
			"required": []string{"to", "message"},
		},
	}
}
