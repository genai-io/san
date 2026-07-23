package command

import (
	_ "embed"
	"fmt"
)

// WrapInvocation envelopes a workflow body in the <custom-command> tag expected
// by the skill-invocation pipeline. Centralizing the envelope keeps
// user-defined custom commands consistent.
func WrapInvocation(name, body string) string {
	return fmt.Sprintf("<custom-command name=%q>\n%s\n</custom-command>", name, body)
}

//go:embed prompts/simplify.md
var simplifyPrompt string

// builtinPromptCommands are slash commands that ship with San as embedded
// markdown workflows rather than Go handlers. They dispatch through the same
// <custom-command> pipeline as user-defined commands, and a user or project
// command with the same name takes precedence — customizing a shipped
// workflow is just dropping a file into .san/commands/.
func builtinPromptCommands() []CustomCommand {
	return []CustomCommand{
		{
			Name:        "simplify",
			Description: "Review the changed code with 4 parallel cleanup agents (reuse, simplification, efficiency, altitude), then apply the fixes",
			Body:        simplifyPrompt,
			Scope:       scopeBuiltin,
		},
	}
}
