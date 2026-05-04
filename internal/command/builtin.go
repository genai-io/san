package command

import (
	"embed"
	"fmt"
	"strings"
)

// Embedded workflow templates for built-in commands that inject hidden
// instructions (e.g. /identity create, /identity edit). These are markdown
// bodies, not full slash commands — they're loaded by handlers on demand
// and pushed through the same skill-invocation pipeline as user-defined
// custom commands.
//
//go:embed builtin/*.md
var builtinFS embed.FS

// BuiltinWorkflow returns the body of a built-in workflow template by name
// (e.g. "identity-create", "identity-edit"). Returns "" if not found.
//
// $ARGUMENTS substitution is the caller's responsibility — the template
// content is returned verbatim.
func BuiltinWorkflow(name string) string {
	data, err := builtinFS.ReadFile("builtin/" + name + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WrapInvocation envelopes a workflow body in the <custom-command> tag
// expected by the skill-invocation pipeline. Centralizing the envelope
// keeps user-defined commands and built-in identity workflows consistent.
func WrapInvocation(name, body string) string {
	return fmt.Sprintf("<custom-command name=%q>\n%s\n</custom-command>", name, body)
}
