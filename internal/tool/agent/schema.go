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
// usage notes so the LLM sees the available agent names right after the
// opening line.
func agentSchema(agentDirectory string) core.ToolSchema {
	agentDirectory = strings.TrimSpace(agentDirectory)

	var sb strings.Builder
	sb.WriteString("Launch a subagent for complex work that benefits from separate context or parallel execution.\n\n")
	if agentDirectory != "" {
		sb.WriteString("Available agent definitions:\n\n")
		sb.WriteString(agentDirectory)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Use the lightest option that fits: a single Bash or Read call → that tool directly; 3+ non-mutating searches with decisions between them → mode=explore; code changes or multi-file edits → mode=edit.\n\n")
	sb.WriteString("Brief the agent like a colleague who just walked in — it has not seen this conversation. Write a self-contained prompt: the goal and why, what you've ruled out, relevant paths and constraints; for lookups the exact command, for investigations the question. Never delegate understanding: \"based on your findings, fix the bug\" pushes synthesis onto the agent.\n\n")
	sb.WriteString("Notes:\n")
	sb.WriteString("- Launch independent agents concurrently — multiple Agent calls in one message. Run foreground when you need the result to continue; run_in_background only for genuinely independent work (you are notified on completion).\n")
	sb.WriteString("- A result summary is what the agent meant to do, not what it did — verify the actual changes before reporting work done, and summarize results back to the user yourself.")

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
		"name": map[string]any{
			"type":        "string",
			"description": "Choose an available agent, or name a new general-purpose agent for this task. New names are for display only.",
		},
		"run_in_background": map[string]any{
			"type":        "boolean",
			"description": "Set to true to run this agent in the background. You will be notified when it completes.",
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps. Defaults to 500; lower values are raised to 500.",
		},
		"mode": map[string]any{
			"type":        "string",
			"description": "Permission mode for the spawned agent: explore = read-only; edit = can modify files; default = use the named definition's configured mode, or inherit the parent session when name is empty.",
			"enum":        []string{"explore", "edit", "default"},
		},
	},
	"required": []string{"description", "prompt"},
}

// Schema returns the model-facing tool definition for AgentStop.
func (t *AgentStopTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: t.Name(),
		Description: `Stops a running background agent.

Only use the exact task_id returned when that agent was started. This tool cannot stop background Bash commands; use the process-group command reported by Bash instead.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The exact Task ID of the running background agent to stop.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Optional concise reason for stopping the agent.",
				},
			},
			"required": []string{"task_id"},
		},
	}
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
