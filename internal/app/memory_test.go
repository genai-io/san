package app

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"testing"
)

// The turn's memory changes use the same hollow-star marker as reads and
// recalls, with writes naming their store.
func TestMemoryLine(t *testing.T) {
	got := ansi.Strip(memoryLine([]conv.MemoryEntry{
		{Verb: "used", Title: "Go dev", Store: "user"}, {Verb: "updated", Title: "Freeze", Store: "project"},
		{Verb: "used", Title: "Tabs", Store: "user"}, {Verb: "saved", Title: "Pnpm", Store: "user"},
	}, 100))
	if got != "⟡ updated Freeze in project · saved Pnpm to user" {
		t.Errorf("memoryLine = %q", got)
	}
	if got := ansi.Strip(memoryLine([]conv.MemoryEntry{{Verb: "recalled", Title: "Go dev"}}, 100)); got != "" {
		t.Errorf("read-only memory must not produce a turn-end footer: %q", got)
	}
	if memoryLine(nil, 80) != "" {
		t.Error("a turn without memory gets no line")
	}
}
