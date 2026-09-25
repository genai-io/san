// /settings General panel: one radio group for background auto-update.
//
//   - AUTO UPDATE — on / off. On (the default) lets an installer-managed
//     binary install new releases in the background at startup; the new
//     version runs on the next launch. Off stays on the current version.
//
// The setting is opt-out (`autoUpdate`, nil = on) and persists to the user
// settings file. SAN_DISABLE_AUTOUPDATE still turns it off regardless.
package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

// AutoUpdateSavedMsg is emitted after the general panel persists the
// auto-update switch so the app can refresh its settings handle and confirm.
type AutoUpdateSavedMsg struct {
	On bool
}

type autoUpdateOption struct {
	label string
	desc  string
	on    bool
}

var autoUpdateOptions = []autoUpdateOption{
	{label: "On", desc: "install new releases in the background; they run on next launch", on: true},
	{label: "Off", desc: "stay on this version (san update to upgrade by hand)", on: false},
}

type generalPanel struct {
	settings *setting.Settings
	cursor   int
	baseline bool // the effective value on disk, marked "● current"
	saveErr  error
}

func newGeneralPanel(settings *setting.Settings) *generalPanel {
	return &generalPanel{settings: settings}
}

func (p *generalPanel) Title() string { return "general" }

func (p *generalPanel) Enter() {
	p.baseline = p.settings == nil || p.settings.AutoUpdate()
	p.cursor = 0
	if !p.baseline {
		p.cursor = 1
	}
	p.saveErr = nil
}

func (p *generalPanel) Dirty() bool { return autoUpdateOptions[p.cursor].on != p.baseline }

func (p *generalPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
		p.saveErr = nil
	case "down", "j":
		p.cursor = min(p.cursor+1, len(autoUpdateOptions)-1)
		p.saveErr = nil
	case "enter", " ":
		opt := autoUpdateOptions[p.cursor]
		if err := setting.SaveAutoUpdate(opt.on); err != nil {
			p.saveErr = err
			return nil, false
		}
		p.baseline = opt.on
		return func() tea.Msg { return AutoUpdateSavedMsg{On: opt.on} }, true
	}
	return nil, false
}

func (p *generalPanel) HintLine() string { return radioHintLine() }

func (p *generalPanel) Render(width, _ int) string {
	var b strings.Builder
	b.WriteString(renderAppearanceSection("AUTO UPDATE", width))
	b.WriteString("\n\n")
	for i, opt := range autoUpdateOptions {
		b.WriteString(renderRadioRow(opt.label, opt.desc, i == p.cursor, opt.on == p.baseline))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(appearanceDescStyle.Render("Applies to binaries installed by the installer. Takes effect on next launch."))
	b.WriteString("\n")
	if p.saveErr != nil {
		b.WriteString("\n")
		b.WriteString(appearanceErrorStyle.Render("⚠ couldn't save: " + p.saveErr.Error()))
		b.WriteString("\n")
	}
	return b.String()
}
