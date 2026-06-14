package selector

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRenderPreview prints the panel without ANSI codes so the layout
// is visually inspectable via `go test -v -run TestRenderPreview`.
func TestRenderPreview(t *testing.T) {
	c := NewConfig(nil)
	c.Enter(80, 40)
	// Toggle Memory's enable so the preview shows the heavy rail (┃) for
	// the enabled section next to the dashed rail (╎) for the disabled one.
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace})
	for range 3 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	out := c.Render()
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	clean := ansi.ReplaceAllString(out, "")
	if !strings.Contains(clean, "self-learning") {
		t.Fatal("missing title")
	}
	if testing.Verbose() {
		fmt.Println("\n" + clean + "\n")
	}
}
