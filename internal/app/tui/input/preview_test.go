package input

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/setting"
)

// TestRenderPreview prints both Reflection tabs without ANSI codes.
func TestRenderPreview(t *testing.T) {
	cwd, svc := t.TempDir(), setting.New(nil)
	c := NewReflectionSelector(ReflectionSources{Workspace: func() (string, *setting.Settings) { return cwd, svc }})
	c.Enter(50, 25)
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	for _, want := range [][]string{{"Recall", "Update", "options", "esc close · tab panels"}, {"Create new skills"}} {
		clean := ansi.ReplaceAllString(c.Render(), "")
		for _, label := range append([]string{"AUTO SKILL", "AUTO MEMORY"}, want...) {
			if !strings.Contains(clean, label) {
				t.Fatalf("missing %q:\n%s", label, clean)
			}
		}
		if lipgloss.Width(clean) > 50 {
			t.Fatalf("Reflection exceeds 50 columns:\n%s", clean)
		}
		if testing.Verbose() {
			fmt.Println("\n" + clean + "\n")
		}
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	}
}
