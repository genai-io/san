package input

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/setting"
)

// A tool the session holds on shows on over its setting, with why, and the
// setting it would fall back to.
func TestToolSelectorShowsTheRuntimeLayerOverTheSetting(t *testing.T) {
	s := NewToolSelector(
		func(setting.Scope) map[string]bool { return map[string]bool{} },
		func(map[string]bool, setting.Scope) error { return nil },
		func() map[string]string { return map[string]string{"SendMessage": "on while in a group"} },
	)
	if err := s.EnterSelect(120, 60, nil); err != nil {
		t.Fatal(err)
	}
	// This package registers no tools: list the one row under test.
	s.list.load([]toolItem{{Name: "SendMessage", Description: "Send a message to another session."}}, 120, 60)
	for _, line := range strings.Split(xansi.Strip(s.Render()), "\n") {
		if strings.Contains(line, "SendMessage") {
			if !strings.Contains(line, "●") || !strings.Contains(line, "on while in a group · setting: off") {
				t.Errorf("held row = %q", line)
			}
			return
		}
	}
	t.Fatal("SendMessage row not rendered")
}
