package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMemoryUpdateStatusIsTransientAndManualOnly(t *testing.T) {
	const label = "⟡ Updating memory…"
	m := fixedComposerModel(80, 12)
	m.userInput.Textarea.SetValue("draft")
	idle := m.View()

	m.memoryUpdate = &memoryUpdate{manual: true, cancel: func() {}}
	active := m.View()
	plain := ansi.Strip(active.Content)
	if strings.Count(plain, label) != 1 || !strings.Contains(plain, label+"\n─") {
		t.Fatalf("expected one memory update status above the composer rule:\n%s", plain)
	}
	if active.Cursor == nil || idle.Cursor == nil || active.Cursor.Position != idle.Cursor.Position ||
		!strings.Contains(strings.Split(plain, "\n")[active.Cursor.Position.Y], "draft") {
		t.Fatalf("memory update displaced the composer: cursor=%+v\n%s", active.Cursor, plain)
	}

	m.cancelMemoryUpdate()
	if got := m.View().Content; got != idle.Content {
		t.Fatalf("clearing memory update left a visual trace:\n%s", ansi.Strip(got))
	}
	m.memoryUpdate = &memoryUpdate{}
	if got := m.View().Content; got != idle.Content {
		t.Fatalf("automatic update changed the idle view:\n%s", ansi.Strip(got))
	}
}

func TestMemoryUpdateStatusFitsNarrowFooter(t *testing.T) {
	const width = 16
	m := fixedComposerModel(width, 12)
	m.memoryUpdate = &memoryUpdate{manual: true}
	footer, inputRow := m.renderFooter(strings.Repeat("─", width-1), "")
	row := strings.Split(ansi.Strip(footer), "\n")[inputRow-2]
	if !strings.HasPrefix(row, "⟡ Updating") || ansi.StringWidth(row) >= width {
		t.Fatalf("memory update status missing or too wide: %q", row)
	}
}
