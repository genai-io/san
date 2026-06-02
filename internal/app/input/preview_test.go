package input

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRenderPreview prints the panel without ANSI codes so the layout
// is visually inspectable via `go test -v -run TestRenderPreview`.
func TestRenderPreview(t *testing.T) {
	c := NewConfigSelector(nil)
	c.Enter(80, 40)
	for range 3 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	}
	out := c.Render()
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	clean := ansi.ReplaceAllString(out, "")
	if !strings.Contains(clean, "Self-Learning") {
		t.Fatal("missing title")
	}
	if testing.Verbose() {
		fmt.Println("\n" + clean + "\n")
	}
}
