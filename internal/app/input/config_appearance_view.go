package input

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// appearanceBody records positions while generating rows, so windowing does
// not need to reconstruct section spacing or identify errors by their offsets.
type appearanceBody struct {
	lines      []string
	errors     []string
	sectionRow int
	cursorRow  int
}

// Render windows the flat Appearance list around the hovered section. Adding
// preferences must not push the settings card past a normal 24-row terminal.
// Navigation still traverses every option; failed saves remain pinned in view.
func (p *appearancePanel) Render(width, height int) string {
	body := p.renderBody(width)
	lines := body.lines
	if height > 0 && len(lines)+len(body.errors) > height {
		available := max(1, height-len(body.errors))
		start := min(body.sectionRow, max(0, len(lines)-available))
		if body.cursorRow >= start+available {
			start = body.cursorRow - available + 1
		}
		lines = lines[start:min(len(lines), start+available)]
	}
	lines = append(lines, body.errors...)
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, max(0, width), "…")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func (p *appearancePanel) renderBody(width int) appearanceBody {
	body := appearanceBody{lines: make([]string, 0, len(p.options)*2)}
	previous := ""
	sectionRow := 0
	for i, opt := range p.options {
		if opt.section != previous {
			if previous != "" {
				body.lines = append(body.lines, "")
			}
			sectionRow = len(body.lines)
			body.lines = append(body.lines, renderAppearanceSection(opt.section, width))
			previous = opt.section
		}
		if i == p.cursor {
			body.sectionRow = sectionRow
			body.cursorRow = len(body.lines)
		}
		if isAppearanceBar(opt) {
			body.lines = append(body.lines, renderBarToggle(opt, i == p.cursor, width))
		} else {
			body.lines = append(body.lines, renderRadioRow(opt.label, opt.desc, i == p.cursor, p.isCurrent(opt)))
		}
	}
	if p.saveErr != nil {
		body.errors = []string{"", appearanceErrorStyle.Render("⚠ couldn't save: " + p.saveErr.Error())}
	}
	return body
}
