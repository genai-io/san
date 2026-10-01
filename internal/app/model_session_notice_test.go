package app

import (
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/session"
)

// Before the first message there is no recorder; notices wait for it.
func TestNoticesBeforeTheRecorderWaitForIt(t *testing.T) {
	m := &model{services: services{Session: &session.Setup{}}, conv: conv.NewModel(80)}
	m.conv.OnNotice = m.recordNotice
	m.conv.AddNotice("Joined shop as @api")
	if len(m.earlyNotices) != 1 {
		t.Fatalf("early notices = %d, want 1", len(m.earlyNotices))
	}
}
