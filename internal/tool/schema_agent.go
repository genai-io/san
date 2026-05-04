package tool

import "github.com/genai-io/gen-code/internal/core"

// agentToolSchema is the schema for the Agent tool.
var agentToolSchema = core.ToolSchema{
	Name: "Agent",
	Description: `Launch a subagent for complex work that benefits from separate context or parallel execution.

Check <agents> for available agent types and their when-to-use guidance. Use agent name as subagent_type. If omitted, the general-purpose agent is used.

Use direct tools instead for simple reads, narrow searches, or tasks that only need 1-2 tool calls.

Usage notes:
- Always include a short description (3-5 words) summarizing what the agent will do
- Launch multiple agents concurrently whenever possible; to do that, use a single message with multiple Agent calls
- Each agent has isolated context; summarize important results back to the user yourself
- Use foreground by default when you need the result before continuing
- Use run_in_background only for genuinely independent work; you will be notified when it completes
- Provide concrete prompts with file paths, constraints, and whether code changes are expected`,
	Parameters: map[string]any{
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
			"max_turns": map[string]any{
				"type":        "number",
				"description": "Maximum number of conversation turns for the agent. Built-in agents default to 100 and lower values are raised to 100.",
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
	},
}

var sendMessageToolSchema = core.ToolSchema{
	Name: "SendMessage",
	Description: `Send a follow-up message to an existing subagent worker.

Use this when you want to keep steering the same worker instead of spawning a fresh one.

Current runtime behavior:
- Completed or resumable workers: supported
- Currently running workers: not supported; wait for completion before sending a follow-up

Usage notes:
- Prefer task_id when you have a background worker from this conversation
- Use agent_id when you already know the resumable session/agent ID
- When using agent_id directly, also provide subagent_type so the correct agent configuration can be restored
- run_in_background=true resumes the worker asynchronously and returns immediately; you will be automatically notified when it completes — do not poll or check progress`,
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
			"message": map[string]any{
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
			"max_turns": map[string]any{
				"type":        "number",
				"description": "Maximum number of conversation turns for the resumed run. Built-in agents default to 100 and lower values are raised to 100.",
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
		"required": []string{"message"},
	},
}

// skillToolSchema is the schema for the Skill tool.
var skillToolSchema = core.ToolSchema{
	Name: "Skill",
	Description: `Execute a skill within the main conversation.

When users ask to perform tasks, check if available skills can help.
Skills provide specialized capabilities and domain knowledge.

When users reference "/<skill-name>" (e.g., "/commit", "/review-pr"), use this tool to invoke it.

Example:
  User: "run /commit"
  Assistant: [Calls Skill tool with skill: "commit"]

How to invoke:
- skill: "pdf" - invoke the pdf skill
- skill: "commit", args: "-m 'Fix bug'" - invoke with arguments
- skill: "git:pr" - invoke using namespace:name format

Important:
- Available skills are listed in system-reminder messages in the conversation
- When a skill matches the user's request, this is a BLOCKING REQUIREMENT: invoke the relevant Skill tool BEFORE generating any other response about the task
- NEVER mention a skill without actually calling this tool
- Do not invoke a skill that is already running
- Do not use this tool for built-in CLI commands (like /help, /clear, etc.)
- If you see a <command-name> tag in the current conversation turn, the skill has ALREADY been loaded - follow the instructions directly instead of calling this tool again`,
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
