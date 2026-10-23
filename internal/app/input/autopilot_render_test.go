package input

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestAutopilotResizeKeepsFrameWithinTerminal(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(120, 40)

	p.Resize(68, 28)
	rendered := p.Render()

	if got := lipgloss.Width(rendered); got != 68 {
		t.Fatalf("rendered width = %d, want 68", got)
	}
	if got := lipgloss.Height(rendered); got != 26 {
		t.Fatalf("rendered height = %d, want 26", got)
	}
}

func TestAutopilotNarrowFrameDoesNotOverflowTerminal(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(60, 28)

	if got := lipgloss.Width(p.Render()); got != 60 {
		t.Fatalf("rendered width = %d, want 60", got)
	}
}

// The menu is one page: what always runs, then what runs only while it drives,
// and the System row says outright that it is the (editable part of the)
// system prompt.
func TestAutopilotMenuGroups(t *testing.T) {
	p := NewAutopilotSelector()
	p.Enter(120, 40)

	rendered := p.Render()
	for _, want := range []string{"ALWAYS", "Recap", "WHILE IT DRIVES", "shift+tab to drive", "(system prompt)", "Continue", "Load preset…"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("menu is missing %q", want)
		}
	}

	p.openSystemPrompt()
	if !strings.Contains(p.Render(), "Safety rules are fixed") {
		t.Error("the system prompt editor does not say the safety rules are fixed")
	}
}
