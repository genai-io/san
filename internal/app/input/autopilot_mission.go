// Mission dialog: a mission editor. The text box holds the whole mission and is
// the mission — you type, paste, and edit it (enter saves it, alt+enter inserts a
// newline). That is the plain "input" path. The "input + thinking" path is ctrl+r:
// the copilot (an injected MissionRefiner) rewrites the box into a cleaner, more
// complete mission, shown in place. ctrl+c clears; esc saves and leaves. The core
// actions all use keys every terminal delivers.
package input

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// MissionRefiner rewrites a mission draft into an improved mission. The app
// injects one backed by the session model; a nil refiner disables the ctrl+r
// refine action (editing the mission still works).
type MissionRefiner func(ctx context.Context, draft string) (string, error)

// MissionRefinedMsg carries a refined mission (or error) back to the panel; the
// app routes it to AutopilotSelector.DeliverRefinedMission.
type MissionRefinedMsg struct {
	Mission string
	Err     error
}

// AutopilotMissionSavedMsg carries the mission the editor just saved (empty when
// cleared) so the app persists it to the live session at once — the editor's
// save/clear don't wait for the panel's Save button.
type AutopilotMissionSavedMsg struct{ Mission string }

type missionDialog struct {
	input    textarea.Model // holds the whole mission; editing it IS the mission
	spinner  spinner.Model
	refining bool
	status   string // transient notice/error under the editor
}

func newMissionDialog() missionDialog {
	ta := newChromelessTextarea()
	ta.Placeholder = "Describe the mission: what should the copilot get done this session?"
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: kit.StarSpinnerFrames, FPS: kit.StarSpinnerFPS}
	sp.Style = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
	return missionDialog{input: ta, spinner: sp}
}

// missionEditorHeight is how many lines the mission editor shows — enough to
// draft a short paragraph without dominating the card.
const missionEditorHeight = 6

// resetMission seeds the editor from the persisted mission so re-opening the
// panel shows the current mission, ready to edit.
func (p *AutopilotSelector) resetMission() {
	p.mission.refining = false
	p.mission.status = ""
	p.mission.input.SetValue(strings.TrimSpace(p.snap.Mission))
	p.mission.input.CursorEnd()
}

// enterMission focuses and sizes the editor when the view opens. The width leaves
// room for the focus rail renderComposer prefixes each line with.
func (p *AutopilotSelector) enterMission() {
	p.mission.input.SetWidth(p.innerWidth() - missionRailWidth)
	p.mission.input.SetHeight(missionEditorHeight)
	p.mission.input.Focus()
	p.mission.input.CursorEnd()
}

func (p *AutopilotSelector) handleMissionKey(msg tea.KeyMsg) tea.Cmd {
	// While a refine is in flight the box is about to be replaced — take only esc,
	// so keystrokes can't race the reply into a lost edit.
	if p.mission.refining && msg.String() != "esc" {
		return nil
	}
	switch msg.String() {
	case "esc":
		p.mission.input.Blur()
		p.view = apMenu
		return p.saveMission("")
	case "enter":
		// Save (cache) the current mission without leaving the editor.
		return p.saveMission("mission saved")
	case "alt+enter", "shift+enter":
		p.mission.input.InsertString("\n")
		return nil
	case "ctrl+r":
		return p.refineMission()
	case "ctrl+c":
		// The overlay captures ctrl+c (it never reaches the global quit here), so
		// give it a purpose: wipe the mission — this session and the persisted one.
		p.mission.input.SetValue("")
		p.mission.input.CursorEnd()
		return p.saveMission("mission cleared")
	default:
		var cmd tea.Cmd
		p.mission.input, cmd = p.mission.input.Update(msg)
		return cmd
	}
}

// saveMission commits the editor to the working buffer AND emits the mission so
// the app persists it straight to the live session — the editor's save/clear take
// effect at once, not only when the panel's Save button is pressed. An optional
// status note is shown under the editor.
func (p *AutopilotSelector) saveMission(status string) tea.Cmd {
	p.commitMission()
	p.mission.status = status
	mission := p.snap.Mission
	return func() tea.Msg { return AutopilotMissionSavedMsg{Mission: mission} }
}

// refineMission (ctrl+r) is the "input + thinking" path: it hands the current
// draft to the copilot to rewrite into a cleaner mission, behind a spinner. A nil
// refiner (no model) leaves the draft untouched with a note.
func (p *AutopilotSelector) refineMission() tea.Cmd {
	draft := strings.TrimSpace(p.mission.input.Value())
	if draft == "" {
		return nil
	}
	if p.refine == nil {
		p.mission.status = "no copilot model to refine with"
		return nil
	}
	p.mission.status = ""
	p.mission.refining = true
	refine := p.refine
	request := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		refined, err := refine(ctx, draft)
		return MissionRefinedMsg{Mission: refined, Err: err}
	}
	return tea.Batch(p.mission.spinner.Tick, request)
}

// commitMission persists the editor content as the mission directive.
func (p *AutopilotSelector) commitMission() {
	p.snap.Mission = strings.TrimSpace(p.mission.input.Value())
}

// DeliverRefinedMission is called by the app when a MissionRefinedMsg arrives:
// the refined mission replaces the editor content. On error the draft is kept
// as-is so a failed refine never loses the mission.
func (p *AutopilotSelector) DeliverRefinedMission(mission string, err error) {
	p.mission.refining = false
	if err != nil {
		p.mission.status = "copilot error: " + err.Error()
		return
	}
	if m := strings.TrimSpace(mission); m != "" {
		p.mission.input.SetValue(m)
		p.mission.input.CursorEnd()
		p.commitMission()
	}
}

// Thinking reports whether the mission dialog is awaiting a reply — the app
// gates spinner ticks on this.
func (p *AutopilotSelector) Thinking() bool {
	return p.active && p.view == apMission && p.mission.refining
}

// UpdateSpinner advances the mission spinner and returns its next tick.
func (p *AutopilotSelector) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.mission.spinner, cmd = p.mission.spinner.Update(msg)
	return cmd
}

func (p *AutopilotSelector) missionHint() string {
	return kit.HintLine(keycap("enter")+" save", keycap("ctrl+r")+" refine", keycap("ctrl+c")+" clear", keycap("esc")+" done")
}

func (p *AutopilotSelector) renderMission() string {
	var b strings.Builder
	b.WriteString(p.renderComposer())
	if p.mission.refining {
		b.WriteString("\n")
		b.WriteString(p.mission.spinner.View() + " " + apDescStyle.Render("refining…"))
	}
	if p.mission.status != "" {
		b.WriteString("\n")
		b.WriteString(apDescStyle.Render(p.mission.status))
	}
	return b.String()
}

// missionRailWidth is the column the focus rail (+ its gap) occupies to the left
// of the editor; enterMission subtracts it so the railed editor still fits.
const missionRailWidth = 2

// renderComposer draws the mission editor with the shared focus rail
// (kit.FocusBar) down its left edge, so it reads as the active field — the same
// affordance every selectable list row uses. Kept minimal — one thin bar, no box.
func (p *AutopilotSelector) renderComposer() string {
	rail := kit.FocusBarStyle().Render(kit.FocusBar) + " "
	lines := strings.Split(p.mission.input.View(), "\n")
	for i, ln := range lines {
		lines[i] = rail + ln
	}
	return strings.Join(lines, "\n")
}
