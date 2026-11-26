// Interactive-mode splash: a single-line brand mark + cwd-basename + model,
// nothing else. Rendered to a string and shown in the live view from launch
// (see view.go's liveWelcome), then frozen into scrollback on the first commit
// (see model_scrollback.go). Drawing it live keeps it visible immediately while
// still letting it reflect the model the user actually picks rather than
// freezing whatever was selected at launch. Bubbletea draws inline, so the
// committed splash stays in scrollback above the live view.
package tui

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/genai-io/san/internal/app/tui/widgets"
)

type welcomeInfo struct {
	Model string
	CWD   string
}

// welcomeBanner returns the splash as a string for deferred emission into
// scrollback. Falls back to plain text when stdout is not a TTY or NO_COLOR
// is set. The leading newline matches a rendered message, so the banner spaces
// correctly when prepended to the first commit.
func welcomeBanner(info welcomeInfo) string {
	if !welcomeUseColor() {
		return plainWelcome(info)
	}
	return renderWelcome(info)
}

func welcomeUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

var (
	brandBracketStyle = lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Text)
	brandWordStyle    = brandBracketStyle.Bold(true)
	brandStarStyle    = lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Focus)
)

// brandMark keeps the startup and loading mark neutral except for the star,
// which shares the input prompt's accent.
func brandMark() string {
	return brandBracketStyle.Render("<") + " " + brandWordStyle.Render("SAN") + " " + brandStarStyle.Render("✦") + " " + brandBracketStyle.Render("/>")
}

func renderWelcome(info welcomeInfo) string {
	dim := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Muted)

	parts := []string{brandMark()}
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

func plainWelcome(info welcomeInfo) string {
	parts := []string{"< SAN ✦ />"}
	if proj := projectName(info.CWD); proj != "" {
		parts = append(parts, proj)
	}
	if info.Model != "" {
		parts = append(parts, info.Model)
	}
	return "\n" + strings.Join(parts, "  ·  ")
}
