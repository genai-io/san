package input

import (
	"errors"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/setting"
)

// A tool the session holds on shows on over its setting, with why, and the
// setting it would fall back to.
func TestToolSelectorShowsTheRuntimeLayerOverTheSetting(t *testing.T) {
	s := NewToolSelector(
		func(setting.Scope) map[string]bool { return map[string]bool{"SendMessage": true} },
		func(string, *bool, setting.Scope) error { return nil },
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

func TestToolToggleSaveFailure(t *testing.T) {
	s := NewToolSelector(func(setting.Scope) map[string]bool { return map[string]bool{"demo": true} }, func(string, *bool, setting.Scope) error { return errors.New("read-only") }, nil)
	if err := s.EnterSelect(120, 60, nil); err != nil {
		t.Fatal(err)
	}
	s.list.load([]toolItem{{Name: "demo"}}, 120, 60)
	if s.Toggle() != nil || !s.effectiveDisabled(s.activeScope(), "demo") {
		t.Fatal("failed save reported or applied toggle")
	}
	if !strings.Contains(xansi.Strip(s.Render()), "Could not save demo setting: read-only") {
		t.Fatal("missing save error")
	}
}
