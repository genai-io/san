package conv

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestCompactStatusIsQuietBoundedAndTransient(t *testing.T) {
	state := CompactState{Active: true, AutoContinue: true, Count: 83, StartedAt: time.Now().Add(-42 * time.Second)}
	row := ansi.Strip(RenderCompactStatus(100, "●", state))
	for _, want := range []string{"Compacting context", "will continue automatically", "42s"} {
		if !strings.Contains(row, want) {
			t.Fatalf("missing %q in %q", want, row)
		}
	}
	if strings.Contains(row, "83") {
		t.Fatalf("message count leaked into status: %q", row)
	}
	for _, width := range []int{1, 20, 40} {
		if row := RenderCompactStatus(width, "●", state); ansi.StringWidth(row) > width || strings.Contains(row, "\n") {
			t.Fatalf("unbounded status: %q", row)
		}
	}
	state.Clear()
	if row := RenderCompactStatus(100, "●", state); row != "" || state.AutoContinue || !state.StartedAt.IsZero() {
		t.Fatalf("recovery state survived clear: %+v, %q", state, row)
	}
	state.Active = true
	if row := RenderCompactStatus(100, "●", state); strings.Contains(row, "automatically") {
		t.Fatalf("manual compaction promises continuation: %q", row)
	}
	state.Complete("summary failed", true)
	if row := RenderCompactStatus(100, "●", state); !strings.Contains(row, "summary failed") {
		t.Fatalf("terminal failure hidden: %q", row)
	}
}
