package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/transcript"
)

// The turn's memory changes use the same hollow-star marker as reads and
// recalls.
func TestMemoryLine(t *testing.T) {
	got := ansi.Strip(memoryLine([]transcript.MemoryEntry{
		{Verb: "used", Title: "Go dev"}, {Verb: "updated", Title: "Freeze"},
		{Verb: "used", Title: "Tabs"}, {Verb: "saved", Title: "Pnpm"},
	}, 100))
	if got != "⟡ updated Freeze · saved Pnpm" {
		t.Errorf("memoryLine = %q", got)
	}
	if got := ansi.Strip(memoryLine([]transcript.MemoryEntry{{Verb: "recalled", Title: "Go dev"}}, 100)); got != "" {
		t.Errorf("read-only memory must not produce a turn-end footer: %q", got)
	}
	if memoryLine(nil, 80) != "" {
		t.Error("a turn without memory gets no line")
	}
}
