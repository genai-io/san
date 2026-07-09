// Mission dialog: an evolving mission editor. The first message sets the mission
// verbatim — your words are the mission. Each later message refines the running
// draft rather than piling on: the copilot (an injected MissionRefiner) folds
// the new instruction into the current draft and returns the improved mission,
// shown at once behind a spinner. The current draft is the mission text persisted
// with the config; with no model wired, later messages fold in verbatim so
// briefing still works offline.
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

// MissionRefiner refines the mission: given the current draft and the user's new
// instruction, it returns the improved mission. The app injects one backed by the
// session model; a nil refiner disables refinement (the instruction is then folded
// in verbatim).
type MissionRefiner func(ctx context.Context, current, instruction string) (string, error)

// MissionRefinedMsg carries the refined mission (or an error) back to the panel;
// the app routes it to AutopilotSelector.DeliverRefinedMission.
type MissionRefinedMsg struct {
	Mission string
	Err     error
}

type missionDialog struct {
	draft              string // the running mission; the first message sets it, later ones refine it
	pendingInstruction string // the instruction currently being refined (shown while it runs)
	input              textarea.Model
	spinner            spinner.Model
	refining           bool
	status             string // transient notice/error under the composer
}

func newMissionDialog() missionDialog {
	ta := newChromelessTextarea()
	ta.Placeholder = "Brief the copilot: what should it get done this session?"
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: kit.StarSpinnerFrames, FPS: kit.StarSpinnerFPS}
	sp.Style = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
	return missionDialog{input: ta, spinner: sp}
}

// resetMission seeds the dialog from the persisted mission so re-opening the
// panel shows the current draft, ready to refine.
func (p *AutopilotSelector) resetMission() {
	p.mission.draft = strings.TrimSpace(p.snap.Mission)
	p.mission.pendingInstruction = ""
	p.mission.refining = false
	p.mission.status = ""
	p.mission.input.Reset()
}

// enterMission focuses and sizes the composer when the view opens. The width
// leaves room for the focus rail renderComposer prefixes each line with.
func (p *AutopilotSelector) enterMission() {
	p.setMissionPlaceholder()
	p.mission.input.SetWidth(p.innerWidth() - missionRailWidth)
	p.mission.input.SetHeight(3)
	p.mission.input.Focus()
	p.mission.input.CursorEnd()
}

// setMissionPlaceholder switches the composer hint between "brief" (no mission
// yet) and "refine" (a draft exists), so the empty-composer prompt tracks what
// the next enter will do. Called wherever the draft's presence changes.
func (p *AutopilotSelector) setMissionPlaceholder() {
	if p.mission.draft == "" {
		p.mission.input.Placeholder = "Brief the copilot: what should it get done this session?"
	} else {
		p.mission.input.Placeholder = "Refine the mission — e.g. \"also run the tests before pushing\"…"
	}
}

func (p *AutopilotSelector) handleMissionKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.commitMission()
		p.mission.input.Blur()
		p.view = apMenu
		return nil
	case "enter":
		return p.submitMission()
	case "alt+enter", "shift+enter":
		p.mission.input.InsertString("\n")
		return nil
	case "ctrl+r":
		p.clearMission()
		return nil
	default:
		var cmd tea.Cmd
		p.mission.input, cmd = p.mission.input.Update(msg)
		return cmd
	}
}

// submitMission consumes the composer. The first message sets the mission
// verbatim; a later one refines the running draft — via the copilot when a model
// is wired (behind a spinner), or folded in verbatim when it isn't.
func (p *AutopilotSelector) submitMission() tea.Cmd {
	text := strings.TrimSpace(p.mission.input.Value())
	if text == "" || p.mission.refining {
		return nil
	}
	p.mission.input.Reset()
	p.mission.status = ""

	// First message: your words are the mission, verbatim — no round-trip.
	if p.mission.draft == "" {
		p.mission.draft = text
		p.commitMission()
		p.setMissionPlaceholder()
		return nil
	}

	// A later message refines the existing draft. Without a model we can't refine,
	// so fold it in as typed.
	if p.refine == nil {
		p.mission.draft += "\n" + text
		p.commitMission()
		p.mission.status = "no copilot model — added to the mission as typed"
		return nil
	}

	p.mission.refining = true
	p.mission.pendingInstruction = text
	current := p.mission.draft
	refine := p.refine
	request := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		refined, err := refine(ctx, current, text)
		return MissionRefinedMsg{Mission: refined, Err: err}
	}
	return tea.Batch(p.mission.spinner.Tick, request)
}

// clearMission resets the mission to empty (the draft and the composer) and
// persists the cleared mission.
func (p *AutopilotSelector) clearMission() {
	p.mission.draft = ""
	p.mission.pendingInstruction = ""
	p.mission.refining = false
	p.mission.input.Reset()
	p.mission.status = "mission cleared"
	p.commitMission()
	p.setMissionPlaceholder()
}

// commitMission persists the current draft as the mission directive.
func (p *AutopilotSelector) commitMission() {
	p.snap.Mission = strings.TrimSpace(p.mission.draft)
}

// DeliverRefinedMission is called by the app when a MissionRefinedMsg arrives:
// the refined mission replaces the draft. On error the draft is kept as-is so a
// failed refine never loses the mission.
func (p *AutopilotSelector) DeliverRefinedMission(mission string, err error) {
	p.mission.refining = false
	p.mission.pendingInstruction = ""
	if err != nil {
		p.mission.status = "copilot error: " + err.Error()
		return
	}
	if m := strings.TrimSpace(mission); m != "" {
		p.mission.draft = m
		p.commitMission()
	}
}

// Refining reports whether a mission refine is in flight — the app gates spinner
// ticks on this.
func (p *AutopilotSelector) Refining() bool {
	return p.active && p.view == apMission && p.mission.refining
}

// UpdateSpinner advances the mission spinner and returns its next tick.
func (p *AutopilotSelector) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.mission.spinner, cmd = p.mission.spinner.Update(msg)
	return cmd
}

func (p *AutopilotSelector) missionHint() string {
	action := "set mission"
	if p.mission.draft != "" {
		action = "refine"
	}
	return kit.HintLine(keycap("enter")+" "+action, keycap("alt+enter")+" newline", keycap("ctrl+r")+" clear", keycap("esc")+" back")
}

func (p *AutopilotSelector) renderMission(width int) string {
	body := lipgloss.NewStyle().Width(width)
	var b strings.Builder

	switch {
	case p.mission.draft != "":
		// The evolving mission, shown in place so each refine reads as an edit.
		b.WriteString(missionHeadingStyle.Render("⏵ mission"))
		b.WriteString("\n")
		b.WriteString(body.Render(p.mission.draft))
		b.WriteString("\n\n")
	case !p.mission.refining:
		b.WriteString(apDescStyle.Render("Tell the copilot what to accomplish. Your first message is the mission; each one after refines it."))
		b.WriteString("\n\n")
	}

	if p.mission.refining {
		if p.mission.pendingInstruction != "" {
			b.WriteString(missionUserStyle.Render("refine"))
			b.WriteString("\n")
			b.WriteString(body.Render(p.mission.pendingInstruction))
			b.WriteString("\n")
		}
		b.WriteString(p.mission.spinner.View() + " " + apDescStyle.Render("refining…"))
		b.WriteString("\n\n")
	}

	b.WriteString(apRuleStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")
	b.WriteString(p.renderComposer())
	if p.mission.status != "" {
		b.WriteString("\n")
		b.WriteString(apDescStyle.Render(p.mission.status))
	}
	return b.String()
}

// missionRailWidth is the column the focus rail (+ its gap) occupies to the left
// of the composer; enterMission subtracts it so the railed composer still fits.
const missionRailWidth = 2

// renderComposer draws the mission textarea with a focus-accent rail down its
// left edge, so the input reads as the active field you type into rather than
// bare text under the divider. Kept deliberately minimal — one thin bar, no box.
func (p *AutopilotSelector) renderComposer() string {
	rail := missionRailStyle.Render("▎") + " "
	lines := strings.Split(p.mission.input.View(), "\n")
	for i, ln := range lines {
		lines[i] = rail + ln
	}
	return strings.Join(lines, "\n")
}

var (
	missionUserStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)
	missionHeadingStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
	// missionRailStyle is the thin accent bar down the composer's left edge — the
	// app's "you are here / active" focus color, applied sparingly.
	missionRailStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Focus)
)
