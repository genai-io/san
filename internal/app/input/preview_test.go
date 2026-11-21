package input

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRenderPreview prints both Reflection tabs without ANSI codes.
func TestRenderPreview(t *testing.T) {
	c := NewReflectionSelector(ReflectionDeps{})
	c.Enter(80, 40)
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	for _, want := range []string{"Browse files and stores", "Create new skills"} {
		clean := ansi.ReplaceAllString(c.Render(), "")
		if !strings.Contains(clean, "AUTO SKILL") || !strings.Contains(clean, "AUTO MEMORY") || !strings.Contains(clean, want) {
			t.Fatalf("missing Reflection tabs or %q:\n%s", want, clean)
		}
		if testing.Verbose() {
			fmt.Println("\n" + clean + "\n")
		}
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	}
}
