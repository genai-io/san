package tui

import (
	"testing"

	"github.com/genai-io/san/internal/app/tui/chat"
)

// Before the first message there is no writer; notices wait for it.
func TestNoticesBeforeTheWriterWaitForIt(t *testing.T) {
	m := &model{features: appFeatures{Session: nil}, conv: chat.NewModel(80)}
	m.conv.OnNotice = m.recordNotice
	m.conv.AddNotice("Joined shop as @api")
	if len(m.earlyNotices) != 1 {
		t.Fatalf("early notices = %d, want 1", len(m.earlyNotices))
	}
}
