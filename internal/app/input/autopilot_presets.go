// Export / Import sub-views for the /autopilot panel. Export names the current
// config and writes it as a preset under ~/.san/autopilot/<name>.json; Import
// lists those presets and loads the chosen one into the working buffer. Both are
// a shared, non-session space so a copilot config can be reused and shared.
package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/setting"
)

// ── Export (name input) ─────────────────────────────────────────────────

func (p *AutopilotSelector) beginExport() {
	p.nameBuffer = ""
	p.status = ""
	p.view = apExport
}

func (p *AutopilotSelector) handleExportKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.view = apMenu
	case "enter":
		path, err := setting.ExportAutoPilot(p.nameBuffer, p.snap)
		if err != nil {
			p.status = "export failed: " + err.Error()
		} else {
			p.status = "exported → " + kit.ShortenPath(path)
		}
		p.view = apMenu
	case "backspace":
		if r := []rune(p.nameBuffer); len(r) > 0 {
			p.nameBuffer = string(r[:len(r)-1])
		}
	default:
		// Accept typed text; the name is sanitized to a bare filename on save.
		if t := msg.Key().Text; t != "" && len(p.nameBuffer) < 60 {
			p.nameBuffer += t
		}
	}
	return nil
}

func (p *AutopilotSelector) renderExport() string {
	dir := kit.ShortenPath(setting.AutoPilotPresetDir())
	var b strings.Builder
	b.WriteString(apDescStyle.Render("Save the copilot config — Steering Prompt, mission, and steers — as a preset under " + dir + "/"))
	b.WriteString("\n\n")
	b.WriteString(apLabelStyle.Render("Name") + "  ")
	b.WriteString(apValueStyle.Render(p.nameBuffer) + apCursorStyle.Render("_"))
	if strings.TrimSpace(p.nameBuffer) != "" {
		b.WriteString("\n\n")
		b.WriteString(apSummaryStyle.Render("→ " + p.nameBuffer + ".json"))
	}
	return b.String()
}

func (p *AutopilotSelector) exportHint() string {
	return kit.HintLine(keycap("enter")+" save", keycap("esc")+" cancel")
}

// ── Import (preset list) ────────────────────────────────────────────────

func (p *AutopilotSelector) beginImport() {
	names, err := setting.ListAutoPilotPresets()
	if err != nil {
		p.status = "import failed: " + err.Error()
		return
	}
	p.presets = names
	p.importCursor = 0
	p.status = ""
	p.view = apImport
}

func (p *AutopilotSelector) handleImportKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.view = apMenu
	case "up", "k":
		if p.importCursor > 0 {
			p.importCursor--
		}
	case "down", "j":
		if p.importCursor < len(p.presets)-1 {
			p.importCursor++
		}
	case "enter":
		if len(p.presets) == 0 {
			p.view = apMenu
			return nil
		}
		name := p.presets[p.importCursor]
		cfg, err := setting.ImportAutoPilot(name)
		if err != nil {
			p.status = "import failed: " + err.Error()
		} else {
			p.snap = cfg.Clone()
			p.resetMission()
			p.reclampCursor()
			p.status = "imported ← " + name
		}
		p.view = apMenu
	}
	return nil
}

func (p *AutopilotSelector) renderImport() string {
	if len(p.presets) == 0 {
		return apDescStyle.Render("No presets yet — Export one first.")
	}
	var b strings.Builder
	b.WriteString(apDescStyle.Render("Load a saved preset into the panel, then Save to apply it (steers become the new-session default; Steering Prompt and mission stay with this session):"))
	b.WriteString("\n\n")
	for i, name := range p.presets {
		mark := "  "
		label := apLabelStyle.Render(name)
		if i == p.importCursor {
			mark = apCursorStyle.Render("▸ ")
			label = apBreadcrumbSubStyle.Render(name)
		}
		b.WriteString(mark + label + "\n")
	}
	return b.String()
}

func (p *AutopilotSelector) importHint() string {
	return kit.HintLine(keycap("↑↓")+" navigate", keycap("enter")+" load", keycap("esc")+" cancel")
}
