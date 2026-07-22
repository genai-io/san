package setting

import "testing"

func TestWithDefaultDisabledToolsOverlay(t *testing.T) {
	// Absent key falls back to the factory default.
	got := WithDefaultDisabledTools(nil)
	if !got["Cron"] {
		t.Fatal("Cron should be disabled by default")
	}

	// An explicit false entry (the /tool panel's enable) overrides the default.
	got = WithDefaultDisabledTools(map[string]bool{"Cron": false})
	if got["Cron"] {
		t.Fatal("explicit enable must override the factory default")
	}

	// Explicit disables of normal tools pass through.
	got = WithDefaultDisabledTools(map[string]bool{"WebSearch": true})
	if !got["WebSearch"] || !got["Cron"] {
		t.Fatalf("unexpected map: %#v", got)
	}
}
