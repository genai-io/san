package flow

import (
	"strings"
	"testing"
)

func TestAuthoringPromptEndsWithVerbatimGoal(t *testing.T) {
	goal := "Inspect \"quotes\"\nexactly"
	got := AuthoringPrompt("Available Skill: x", goal)
	if !strings.HasPrefix(got, "Available Skill: x\n\n") || !strings.HasSuffix(got, "User goal (verbatim):\n"+goal) {
		t.Fatalf("prompt framing changed: %q", got)
	}
}

func TestUnwrapDraft(t *testing.T) {
	for _, wrapper := range []string{"", "```markdown", "````md", "`````"} {
		source := "---\nname: x\n---\n```mermaid\nflowchart LR\n a\n```\n\n## a\nwork"
		wrapped := source
		if wrapper != "" {
			n := len(wrapper) - len(strings.TrimLeft(wrapper, "`"))
			wrapped = wrapper + "\n" + source + "\n" + strings.Repeat("`", n)
		}
		if got := UnwrapDraft(wrapped); got != source {
			t.Fatalf("wrapper %q: %q", wrapper, got)
		}
	}
	mermaid := "```mermaid\nflowchart LR\n a\n```"
	if UnwrapDraft(mermaid) != mermaid {
		t.Fatal("inner mermaid fence removed")
	}
}
