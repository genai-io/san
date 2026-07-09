package input

import (
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
