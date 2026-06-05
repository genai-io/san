package tool

import (
	"strings"

	"github.com/genai-io/gen-code/internal/core"
)

// agentToolSchema returns the Agent tool schema with the given directory body
// embedded directly in the description. The directory is rendered before the
// usage notes so the LLM sees the available agent types right after the
// opening line. An empty directory yields a directory-less description that
// still mentions subagent_type — useful for subagent contexts where the
// directory is intentionally omitted to discourage recursive spawning.
func agentToolSchema(directory string) core.ToolSchema {
	directory = strings.TrimSpace(directory)

	var sb strings.Builder
	sb.WriteString("Launch a subagent for complex work that benefits from separate context or parallel execution.\n\n")
	if directory != "" {
		sb.WriteString(directory)
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
			"description": "Optional model override. If omitted, inherits from parent conversation.",
			"enum":        []string{"sonnet", "opus", "haiku"},
		},
		"max_steps": map[string]any{
			"type":        "number",
			"description": "Maximum number of LLM inference steps for the agent. Built-in agents default to 100 and lower values are raised to 100.",
		},
		"resume": map[string]any{
			"type":        "string",
			"description": "Agent ID to resume from a previous invocation.",
		},
		"mode": map[string]any{
			"type":        "string",
			"description": "Permission mode for spawned agent.",
			"enum":        []string{"explore", "edit", "default"},
		},
		"isolation": map[string]any{
			"type":        "string",
			"description": "Isolation mode for the agent.",
			"enum":        []string{"worktree"},
		},
	},
	"required": []string{"description", "prompt"},
}

var sendMessageToolSchema = core.ToolSchema{
	Name: "SendMessage",
	Description: `Send a follow-up message to an existing subagent worker.

Use this when you need to provide additional input or guidance to a worker after it has started running. Routes to the running agent (preferred via task_id), or resumes a paused agent (via agent_id + subagent_type).

Notes:
- Prefer task_id when the worker is still running — the message is delivered without a fresh agent boot.
- Use agent_id only when resuming a paused/saved agent; you must include subagent_type so the right configuration is restored.
- Use run_in_background to detach the resumed run, mirroring the Agent tool's flag.
- The agent receives the message as a fresh user turn — provide enough context for it to act on.
- When using agent_id directly, also provide subagent_type so the correct agent configuration can be restored`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Background task ID for the worker you want to message. Preferred when available. Provide either task_id, or agent_id with subagent_type.",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Resumable agent/session ID to continue directly. When using agent_id, subagent_type is required.",
			},
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Agent type to use when resuming by agent_id directly.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The follow-up message to send to the worker.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "A short (3-5 word) description of what this follow-up asks the worker to do.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional display name override for the continued worker.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Set to true to continue the worker in the background. You will be notified when it completes.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override. If omitted, inherits from parent conversation.",
				"enum":        []string{"sonnet", "opus", "haiku"},
			},
			"max_steps": map[string]any{
				"type":        "number",
				"description": "Maximum number of LLM inference steps for the resumed run. Built-in agents default to 100 and lower values are raised to 100.",
			},
			"mode": map[string]any{
				"type":        "string",
				"description": "Permission mode for the resumed worker.",
				"enum":        []string{"explore", "edit", "default"},
			},
			"isolation": map[string]any{
				"type":        "string",
				"description": "Isolation mode for the resumed worker.",
				"enum":        []string{"worktree"},
			},
		},
		"required": []string{"prompt", "description"},
	},
}

// skillToolSchema is the schema for the Skill tool.
var skillToolSchema = core.ToolSchema{
	Name: "Skill",
	Description: `Execute a skill within the main conversation.

When users ask you to perform tasks, check if any of the available skills match. Skills provide specialized capabilities and domain knowledge.

When users reference a "slash command" or "/<something>", they are referring to a skill. Use this tool to invoke it.

How to invoke:
- Set ` + "`skill`" + ` to the exact name of an available skill (no leading slash). For plugin-namespaced skills use the fully qualified ` + "`plugin:skill`" + ` form.
- Set ` + "`args`" + ` to pass optional arguments.

Important:
- Available skills are listed in <system-reminder> messages in the conversation; only invoke a skill that appears there.
- When a skill matches the user's request, this is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task.
- Do not invoke a skill that is already running.
- Do not use this tool for built-in CLI commands (like /help, /clear, etc.).
- If the current user message starts with a <command-name>...</command-name> tag, the skill body has ALREADY been inlined inside a <skill-invocation> block — follow those instructions directly instead of calling this tool again.
`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "The skill name (e.g., 'commit', 'git:pr', 'pdf')",
			},
			"args": map[string]any{
				"type":        "string",
				"description": "Optional arguments for the skill",
			},
		},
		"required": []string{"skill"},
	},
}
