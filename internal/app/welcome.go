// Interactive-mode splash: a single-line brand mark + cwd-basename + model,
// nothing else. Rendered to a string and shown in the live view from launch
// (see view.go's liveWelcome), then frozen into scrollback on the first commit
// (see model_scrollback.go). Drawing it live keeps it visible immediately while
// still letting it reflect the model the user actually picks rather than
// freezing whatever was selected at launch. Bubbletea draws inline, so the
// committed splash stays in scrollback above the live view.
package app

import (
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
	brandWordStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus).Bold(true)
	brandStarStyle = lipgloss.NewStyle().Foreground(welcomeStar)
)

// brandMark renders the "< SAN ✦ />" wordmark — teal brackets/word (the shared
// Focus accent) with the star-blue ✦. Used by the startup splash, the live
// model-change line, and the cold-start loading line so the brand reads
// identically across all three.
func brandMark() string {
	return brandWordStyle.Render("< SAN") + " " + brandStarStyle.Render("✦") + " " + brandWordStyle.Render("/>")
}

func renderWelcome(info welcomeInfo) string {
	dim := lipgloss.NewStyle().Foreground(welcomeDim)

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
