package tool

import (
	"strings"

	"github.com/genai-io/san/internal/core"
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
	sb.WriteString("- A running background agent can be steered mid-run with SendMessage(task_id); it reports back when done\n")
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
			"description": "Optional model override: an alias (sonnet, opus, haiku), a model id on the current provider, or vendor/model (e.g. deepseek/deepseek-v4) to route to another connected provider. If omitted, inherits from parent conversation.",
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
