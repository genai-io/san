package agent

import (
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

var _ tool.AgentCatalogAwareTool = (*AgentTool)(nil)

// Schema returns the single model-facing Agent tool definition.
func (t *AgentTool) Schema() core.ToolSchema {
	return agentSchema("")
}

// SchemaWithAgentCatalog adds predefined-agent discovery metadata.
func (t *AgentTool) SchemaWithAgentCatalog(agentCatalog string) core.ToolSchema {
	return agentSchema(agentCatalog)
}

func agentSchema(agentCatalog string) core.ToolSchema {
	var sb strings.Builder
	sb.WriteString("Handle the task directly by default. Delegate an independently scoped task only when the benefits of context isolation, parallel execution, or distinct capabilities outweigh briefing, duplicated reads, and result verification.\n\n")
	if catalog := strings.TrimSpace(agentCatalog); catalog != "" {
		sb.WriteString(catalog)
		sb.WriteString("\n\nAfter deciding to delegate, use name to select a predefined agent or omit it for the default Agent. Catalog matches describe suitability, not invocation requirements.\n\n")
	}
	sb.WriteString("Brief the agent with all context it needs: the goal, relevant paths, constraints, and what is already known. Include the validation plan in the brief. Do not retry denied commands; report the failed tool and reason to the parent.\n\n")
	sb.WriteString("When delegating multiple independent tasks, launch them concurrently. For new work, use foreground execution when you need the result to proceed, and background execution when you can continue independent work. Do not duplicate delegated work. Verify the result before reporting it.\n\n")
	sb.WriteString("Use SendMessage with the returned Task ID for follow-up; it keeps the original session and policy. Check uncertain side effects before retrying failed work.")

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
			"description": "A self-contained task brief with the goal, relevant paths, constraints, known findings, and validation plan.",
		},
		"description": map[string]any{
			"type":        "string",
			"description": "A short (3-5 word) description of the task",
		},
		"name": map[string]any{
			"type":        "string",
			"description": "Optional name for this agent. New names label the task; an existing custom definition supplies its configuration.",
		},
		"run_in_background": map[string]any{
			"type":        "boolean",
			"description": "Defaults to false for new work: foreground calls in the same batch run concurrently, and the batch waits for all results before your next inference. Set true when you can continue useful independent work without this result; background completion will be notified separately.",
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps. A positive value overrides the execution budget. New work defaults to the selected agent's configured budget (500 unless configured otherwise).",
		},
	},
	"required": []string{"description", "prompt"},
}

// Schema returns the model-facing tool definition for AgentStop.
func (t *AgentStopTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name: t.Name(),
		Description: `Stop a running or idle agent. Later messages cannot restart it.

Only use the exact task_id returned when that agent was started. This tool cannot stop background Bash commands; use the process-group command reported by Bash instead.`,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The exact Task ID of the agent to stop.",
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
		Description: `Send a message to an agent's exact Task ID, "main", or a group member's name.
- Running agents queue messages between steps. Only the owning main conversation can continue an idle or recoverable failed agent, after permission checks. Continuation runs in the background with the original history, tools, model, permissions, and step budget.
- Stopped/killed agents, exhausted recovery budgets, missing history, and unknown IDs return errors; no replacement is created. Task IDs remain usable until process exit.
- Acceptance is not acknowledgment or completion. Verify the result before relying on it.
- Background Bash processes have no agent inbox; inspect their output and use shell tools to manage them.
- Group members appear in the <group> reminder. The result explains whether delivery waits for a step, user input, or session resume.
- Keep messages brief and self-contained. They are attributed to you, not the recipient's user. Report results or blockers when asked; skip greetings and bare acknowledgments.`,
		Definition: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to": map[string]any{
					"type":        "string",
					"description": "An existing agent's exact task ID, \"main\", or a group member's name without \"@\".",
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
