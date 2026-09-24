// /settings General panel: one-line options, each switched in place.
//
//   - Auto update — on / off. On (the default) lets an installer-managed
//     binary install new releases in the background at startup; the new
//     version runs on the next launch.
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

type generalPanel struct {
	settings   *setting.Settings
	autoUpdate bool // the value on disk
	saveErr    error
}

func newGeneralPanel(settings *setting.Settings) *generalPanel {
	return &generalPanel{settings: settings}
}

func (p *generalPanel) Title() string { return "general" }

func (p *generalPanel) Enter() {
	p.autoUpdate = p.settings == nil || p.settings.AutoUpdate()
	p.saveErr = nil
}

// Dirty is always false: every switch saves the moment it flips.
func (p *generalPanel) Dirty() bool { return false }

func (p *generalPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "left", "right", "h", "l", "space", " ", "enter":
		on := !p.autoUpdate
		if err := setting.SaveAutoUpdate(on); err != nil {
			p.saveErr = err
			return nil, false
		}
		p.autoUpdate, p.saveErr = on, nil
		return func() tea.Msg { return AutoUpdateSavedMsg{On: on} }, false
	}
	return nil, false
}

func (p *generalPanel) HintLine() string {
	return keycap("←→") + " switch"
}

func (p *generalPanel) Render(_, _ int) string {
	value := "Off"
	if p.autoUpdate {
		value = "On"
	}
	var b strings.Builder
	b.WriteString(appearanceCursorStyle.Render("▸ Auto update  "))
	b.WriteString(appearanceCursorStyle.Render("‹ " + value + " ›"))
	b.WriteString("  " + appearanceDescStyle.Render("install new releases in the background; runs on next launch"))
	b.WriteString("\n")
	if p.saveErr != nil {
		b.WriteString("\n" + appearanceErrorStyle.Render("⚠ couldn't save: "+p.saveErr.Error()) + "\n")
	}
	return b.String()
}
