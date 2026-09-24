package input

import (
	"context"
	"strings"
	"testing"
)

// /config still opens the settings popup but says it is deprecated.
func TestConfigCommandIsDeprecatedAliasOfSettings(t *testing.T) {
	m := New("", 80, nil, SelectorDeps{})
	c := NewSlashCommandController(SlashCommandEnv{Input: &m, Width: 100, Height: 30})

	notice, _, err := c.handleConfigCommand(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Settings.IsActive() {
		t.Fatal("/config did not open the settings popup")
	}
	if !strings.Contains(notice, "/settings") {
		t.Fatalf("notice = %q, want it to point at /settings", notice)
	}
}
