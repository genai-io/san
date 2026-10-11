package conv

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// GitBarParams is the already-read repository snapshot for the optional footer
// row. Rendering never invokes Git or reads settings.
type GitBarParams struct {
	Width      int
	Branch     string
	Repository string
	Staged     int
	Modified   int
	Untracked  int
	Conflicts  int
	Ahead      int
	Behind     int
}

// RenderGitBar renders one plain-text, width-bounded row with no redundant Git
// prefix or status coloring. Branch and changes take priority over the remote
// label; outside a repository there is no extra footer row.
func RenderGitBar(p GitBarParams) string {
	branch := gitBarText(p.Branch)
	width := p.Width - 1 // leave the terminal's final column untouched
	if branch == "" || width < 8 {
		return ""
	}

	var changes []string
	if p.Staged > 0 {
		changes = append(changes, fmt.Sprintf("+%d", p.Staged))
	}
	if p.Modified > 0 {
		changes = append(changes, fmt.Sprintf("~%d", p.Modified))
	}
	if p.Untracked > 0 {
		changes = append(changes, fmt.Sprintf("?%d", p.Untracked))
	}
	if p.Conflicts > 0 {
		changes = append(changes, fmt.Sprintf("!%d", p.Conflicts))
	}
	separator := " · "
	suffix := ""
	if len(changes) > 0 {
		suffix = separator + strings.Join(changes, " ")
	}
	var sync []string
	if p.Ahead > 0 {
		sync = append(sync, fmt.Sprintf("↑%d", p.Ahead))
	}
	if p.Behind > 0 {
		sync = append(sync, fmt.Sprintf("↓%d", p.Behind))
	}
	if len(sync) > 0 {
		suffix += separator + strings.Join(sync, " ")
	}

	prefix := "  "
	branchWidth := max(1, width-ansi.StringWidth(prefix+suffix))
	line := prefix + ansi.Truncate(branch, branchWidth, "…") + suffix
	remaining := width - ansi.StringWidth(line+separator)
	if repo := gitBarText(p.Repository); repo != "" && remaining >= 4 {
		line += separator + ansi.Truncate(repo, remaining, "…")
	}
	return ansi.Truncate(line, width, "…")
}

func gitBarText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}
