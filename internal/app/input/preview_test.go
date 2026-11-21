package input

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRenderPreview prints the Memory & Skills panel without ANSI codes so the
// layout is visually inspectable via `go test -v -run TestRenderPreview`.
func TestRenderPreview(t *testing.T) {
	c := NewMemorySkillsSelector(MemorySkillsDeps{})
	c.Enter(80, 40)
	for range 3 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	out := c.Render()
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	clean := ansi.ReplaceAllString(out, "")
	if !strings.Contains(clean, "AUTO SKILL") || !strings.Contains(clean, "AUTO MEMORY") {
		t.Fatal("missing learning settings groups")
	}
	if testing.Verbose() {
		fmt.Println("\n" + clean + "\n")
	}
}
