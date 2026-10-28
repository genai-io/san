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
	sb.WriteString("Use SendMessage with the exact Task ID returned by Agent for follow-up instructions, whether the worker is running or idle. It retains the original child session and policy. Inspect uncertain side effects before continuing failed work; never repeat denied commands.")

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
		Description: `Stops an existing agent and prevents subsequent messages from restarting it. Completed or failed agents can also be stopped.

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
		Description: `Send input to an existing subagent by its exact task ID, to "main", or to a group member by name.
- Running subagents read queued messages between steps. The owning main conversation can also send input to an idle or recoverable failed agent: after permission checks it continues in the background with its original history, tools, model, and execution budget. Acceptance is not task completion.
- Stopped/killed agents, exhausted recovery budgets, missing history, and unknown IDs are errors; no replacement agent is created. Child agents can message running peers but cannot wake idle peers. Sessions are retained for this process; completion releases execution resources, not conversation identity.
- A background Bash process has no agent inbox; inspect its output and manage its process lifecycle instead.
- Group members are listed in the <group> reminder. Their result says whether mail waits for a step boundary, user input, or session resume.
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
