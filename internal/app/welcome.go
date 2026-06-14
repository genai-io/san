// Interactive-mode splash: a single-line brand mark + cwd-basename + model,
// nothing else. Called once at startup, before tea.NewProgram(m).Run() —
// Bubbletea draws inline, so the splash stays in scrollback above the
// live view.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/genai-io/san/internal/app/kit"
)

// Three-hue palette: teal for the brand mark (the shared Focus accent, so the
// splash and the live UI's focus affordances are the same color), star blue
// for the ✦ accent inside the logo, dim gray for everything else.
var (
	welcomeStar = kit.AdaptiveColor{Dark: "#7FD4FF", Light: "#0284C7"}
	welcomeDim  = kit.AdaptiveColor{Dark: "#65707A", Light: "#9CA3AF"}
)

type welcomeInfo struct {
	Model string
	CWD   string
}

// printWelcome writes the splash to stdout. Falls back to plain text when
// stdout is not a TTY or NO_COLOR is set.
func printWelcome(info welcomeInfo) {
	if !welcomeUseColor() {
		printWelcomePlain(info)
		return
	}
	fmt.Println(renderWelcome(info))
}

func welcomeUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func renderWelcome(info welcomeInfo) string {
	var (
		star    = lipgloss.NewStyle().Foreground(welcomeStar)
		brand   = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
		bracket = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
		dim     = lipgloss.NewStyle().Foreground(welcomeDim)
	)

	header := bracket.Render("< ") + brand.Render("SAN") + " " + star.Render("✦") + " " + bracket.Render("/>")

	parts := []string{header}
	if proj := projectName(info.CWD); proj != "" {
		parts = append(parts, dim.Render(proj))
	}
	if info.Model != "" {
		parts = append(parts, dim.Render(info.Model))
	}
	return "\n" + strings.Join(parts, dim.Render("  ·  "))
}

// projectName returns a compact, human-friendly label for the working
// directory — basename of the path, with $HOME folded to "~".
func projectName(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
	}
	base := filepath.Base(p)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func printWelcomePlain(info welcomeInfo) {
	parts := []string{"< SAN ✦ />"}
	if proj := projectName(info.CWD); proj != "" {
		parts = append(parts, proj)
	}
	if info.Model != "" {
		parts = append(parts, info.Model)
	}
	fmt.Println()
	fmt.Println(strings.Join(parts, "  ·  "))
}
