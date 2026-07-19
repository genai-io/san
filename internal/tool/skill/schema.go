package skill

import "github.com/genai-io/san/internal/core"

// Schema returns the model-facing tool definition for Skill.
func (t *SkillTool) Schema() core.ToolSchema {
	return core.ToolSchema{
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
}
