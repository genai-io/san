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
	sb.WriteString("Launch a subagent only when separate context or parallel execution materially helps. Handle clear, bounded work directly, even when it requires multiple tool calls.\n\n")
	if agentDirectory != "" {
		sb.WriteString("Available agent definitions:\n\n")
		sb.WriteString(agentDirectory)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Brief the agent with all context it needs: the goal, relevant paths, constraints, and what is already known. Use default for general-purpose work so the worker inherits the parent session's permissions, including YOLO. Select explore explicitly for read-only investigation. Select edit only when you deliberately want narrower file-edit permissions, even under a YOLO parent. Edit permits file edits plus read-only shell commands, not arbitrary shell execution: tests/build require an explicit allowed command or parent validation. Include the validation plan in the brief; shell tool availability does not imply go test or other builds are allowed. Do not retry denied commands.\n\n")
	sb.WriteString("Launch independent agents concurrently. Use background mode only for work that does not block your next step. Once work is delegated, do not also do it yourself; wait for the result. Verify the result before reporting it.")

	return core.ToolSchema{
		Name:        "Agent",
		Description: sb.String(),
		Definition:  agentToolParameters,
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
			"description": "Defaults to false: foreground calls in the same batch run concurrently, and the batch waits for all results before your next inference. Set true only when your next step does not depend on this result; background completion will be notified separately.",
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps. Overrides the agent's budget; defaults to 500 when unset.",
		},
		"mode": map[string]any{
			"type":        "string",
			"description": "Permission mode for the spawned agent. Prefer default for general-purpose work: use the named definition's configured mode, or inherit the parent session (including YOLO) for an unnamed or new display-only agent. explore = explicit read-only ceiling even under YOLO; edit = explicit narrower file-edit permissions plus read-only shell, tests/build require an explicit allowed command or parent validation.",
			"default":     "default",
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
		Definition: map[string]any{
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
		Description: `Send a message to another session in your group, by its member name. The
<group> reminder lists the members.
- The recipient reads it as a message from you, not from its user: make it
  self-contained. It arrives with a From line, and the recipient's roster
  already has your role: don't restate who you are, and skip greetings and
  sign-offs. Keep it brief: what changed, what you need.
- The result says when it will be read: now, between the recipient's steps,
  at its user's next input (it holds messages), or when its session resumes
  (offline).
- When you were asked for something, report back when it is done or can't
  be done. Don't send bare acknowledgements.`,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to": map[string]any{
					"type":        "string",
					"description": "A member's name, without \"@\", e.g. \"api\".",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Optional: one line, under ten words, on what the message is about. The recipient's user sees it; without it they see only who wrote.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message body: just what you have to say.",
				},
			},
			"required": []string{"to", "message"},
		},
	}
}
