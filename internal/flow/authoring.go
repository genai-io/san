package flow

import (
	_ "embed"
	"strings"
)

// authoringInstructions brief a one-shot, no-tools drafting completion. They
// restate the grammar Parse accepts; keep the two in step.
//
//go:embed authoring.md
var authoringInstructions string

// AuthoringPrompt is the drafting request for goal. catalog lists the agents
// and skills a draft may reference.
func AuthoringPrompt(catalog, goal string) string {
	return catalog + "\n\n" + authoringInstructions + goal
}

// UnwrapDraft removes one optional outer Markdown fence, preserving inner
// Mermaid fences (including when the outer wrapper uses four or more ticks).
func UnwrapDraft(source string) string {
	source = strings.TrimSpace(source)
	lines := strings.Split(source, "\n")
	if len(lines) < 2 {
		return source
	}
	first := strings.TrimSpace(lines[0])
	n := len(first) - len(strings.TrimLeft(first, "`"))
	if n < 3 {
		return source
	}
	language := strings.ToLower(strings.TrimSpace(first[n:]))
	if language != "" && language != "markdown" && language != "md" {
		return source
	}
	if strings.TrimSpace(lines[len(lines)-1]) != strings.Repeat("`", n) {
		return source
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
