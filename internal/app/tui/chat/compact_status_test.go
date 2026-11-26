package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestCompactStatusIsQuietBoundedAndTransient(t *testing.T) {
	state := CompactState{Active: true, AutoContinue: true, Count: 83, StartedAt: time.Now().Add(-42 * time.Second)}
	rendered := RenderCompactStatus(100, 0, state)
	row := ansi.Strip(rendered)
	const progress = "✦ Summarizing conversation · 42s"
	if row != progress+" · Response resumes automatically" || rendered == RenderCompactStatus(100, 3, state) {
		t.Fatalf("unexpected compaction status: %q", row)
	}
	if row := ansi.Strip(RenderCompactStatus(32, 0, state)); row != progress {
		t.Fatalf("narrow status should drop the continuation hint, not the timer: %q", row)
	}
	for _, width := range []int{1, 20, 40} {
		if row := RenderCompactStatus(width, 0, state); ansi.StringWidth(row) > width || strings.Contains(row, "\n") {
			t.Fatalf("unbounded status: %q", row)
		}
	}
	state.Clear()
	if row := RenderCompactStatus(100, 0, state); row != "" || state.AutoContinue || !state.StartedAt.IsZero() {
		t.Fatalf("recovery state survived clear: %+v, %q", state, row)
	}
	state.Active = true
	if row := RenderCompactStatus(100, 0, state); strings.Contains(row, "automatically") {
		t.Fatalf("manual compaction promises continuation: %q", row)
	}
	state.Complete("summary failed", true)
	if row := RenderCompactStatus(100, 0, state); !strings.Contains(row, "summary failed") {
		t.Fatalf("terminal failure hidden: %q", row)
	}
}
