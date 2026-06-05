package session

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

// P1 regression: ConvertToEntries must preserve the ChatMessage.ID across
// successive calls. Without this, every save assigns a fresh UUID and the
// append-only persistence path duplicates the entire history each turn.
func Test_ConvertToEntries_preservesChatMessageID(t *testing.T) {
	msgs := []core.ChatMessage{
		{ID: "fixed-1", Role: core.RoleUser, Content: "hello"},
		{ID: "fixed-2", Role: core.RoleAssistant, Content: "hi"},
	}

	first := ConvertToEntries(msgs)
	second := ConvertToEntries(msgs)

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 entries each call, got first=%d second=%d", len(first), len(second))
	}
	for i := range first {
		if first[i].UUID != msgs[i].ID {
			t.Errorf("entry[%d] first call: UUID=%q want %q", i, first[i].UUID, msgs[i].ID)
		}
		if second[i].UUID != msgs[i].ID {
			t.Errorf("entry[%d] second call: UUID=%q want %q", i, second[i].UUID, msgs[i].ID)
		}
	}
}

// ChatMessages without an ID still get a fresh UUID (back-compat for any
// path that constructs ChatMessage without going through conv.Append).
func Test_ConvertToEntries_fallsBackWhenIDMissing(t *testing.T) {
	msgs := []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}
	entries := ConvertToEntries(msgs)
	if len(entries) != 1 || entries[0].UUID == "" {
		t.Fatalf("expected fallback UUID, got %+v", entries)
	}
}
