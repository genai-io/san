package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMemoryMaintenanceStatusIsTransientAndManualOnly(t *testing.T) {
	const label = "⟡ Reviewing…"
	m := fixedComposerModel(80, 12)
	m.userInput.Textarea.SetValue("draft")
	idle := m.View()

	m.memoryReview = &memoryReview{manual: true, cancel: func() {}}
	active := m.View()
	plain := ansi.Strip(active.Content)
	if strings.Count(plain, label) != 1 || !strings.Contains(plain, label+"\n─") {
		t.Fatalf("expected one maintenance status above the composer rule:\n%s", plain)
	}
	if active.Cursor == nil || idle.Cursor == nil || active.Cursor.Position != idle.Cursor.Position ||
		!strings.Contains(strings.Split(plain, "\n")[active.Cursor.Position.Y], "draft") {
		t.Fatalf("maintenance displaced the composer: cursor=%+v\n%s", active.Cursor, plain)
	}

	m.cancelMemoryReview()
	if got := m.View().Content; got != idle.Content {
		t.Fatalf("clearing maintenance left a visual trace:\n%s", ansi.Strip(got))
	}
	m.memoryReview = &memoryReview{}
	if got := m.View().Content; got != idle.Content {
		t.Fatalf("automatic review changed the idle view:\n%s", ansi.Strip(got))
	}
}

func TestMemoryMaintenanceStatusFitsNarrowFooter(t *testing.T) {
	const width = 16
	m := fixedComposerModel(width, 12)
	m.memoryReview = &memoryReview{manual: true}
	footer, inputRow := m.renderFooter(strings.Repeat("─", width-1))
	row := strings.Split(ansi.Strip(footer), "\n")[inputRow-2]
	if !strings.HasPrefix(row, "⟡ Reviewing") || ansi.StringWidth(row) >= width {
		t.Fatalf("maintenance status missing or too wide: %q", row)
	}
}
