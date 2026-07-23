package command

import "fmt"

// WrapInvocation envelopes a workflow body in the <custom-command> tag expected
// by the skill-invocation pipeline. Centralizing the envelope keeps
// user-defined custom commands consistent.
func WrapInvocation(name, body string) string {
	return fmt.Sprintf("<custom-command name=%q>\n%s\n</custom-command>", name, body)
}
