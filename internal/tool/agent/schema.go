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
	sb.WriteString("Launch a subagent only when separate context or parallel execution materially helps. Handle clear, bounded work directly, even when it requires multiple tool calls.\n\n")
	if catalog := strings.TrimSpace(agentCatalog); catalog != "" {
		sb.WriteString(catalog)
		sb.WriteString("\n\nUse name to select a predefined agent. Otherwise, use the default Agent.\n\n")
	}
	sb.WriteString("Brief the agent with all context it needs: the goal, relevant paths, constraints, and what is already known. Include the validation plan in the brief. Do not retry denied commands; report the failed tool and reason to the parent.\n\n")
	sb.WriteString("Launch independent agents concurrently. Use background mode only for work that does not block your next step. Once work is delegated, do not also do it yourself; wait for the result. Verify the result before reporting it.\n\n")
	sb.WriteString("Use SendMessage for additional instructions while a worker is running. To continue an ended or failed worker, set resume_task_id to its exact existing Task ID returned by Agent and provide only a short incremental prompt: its original child session and history are restored internally, not copied through the main conversation. Continuation runs in the background and retains the original task identity and policy. Do not resume deliberately stopped workers or repeat denied commands. Inspect uncertain tool side effects before retrying; if continuation is unavailable, report the blocker rather than silently restarting from scratch.")

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
			"description": "Optional name for this agent. New names label the task; an existing custom definition supplies its configuration.",
		},
		"run_in_background": map[string]any{
			"type":        "boolean",
			"description": "Defaults to false: foreground calls in the same batch run concurrently, and the batch waits for all results before your next inference. Set true only when your next step does not depend on this result; background completion will be notified separately.",
		},
		"resume_task_id": map[string]any{
			"type":        []string{"string", "null"},
			"description": "Existing Task ID returned by Agent to continue the same child session, not an agent name or file path. For new work, omit this field or use null; empty string, new, and /dev/null are accepted as no-session placeholders. Continuation runs in the background with the original worker; prompt contains only the new instruction, not its history. Omit name when continuing.",
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps. Overrides the agent's budget; defaults to 500 when unset.",
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
		Description: `Send a message to a running subagent by its exact task ID, to "main", or to a group member by name.
- A running subagent reads queued messages between steps. Acceptance is not an acknowledgment or task completion. An ended worker is not restarted by messaging: use Agent with resume_task_id and a short continuation prompt to restore its original task/session.
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
					"description": "A running agent's exact task ID, \"main\", or a group member's name without \"@\".",
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
