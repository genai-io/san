// Mission dialog: a lightweight two-way briefing where the user tells the
// copilot what to accomplish this session and the copilot confirms how it plans
// to drive. Replies are non-streaming — the panel shows a spinner while the
// injected MissionResponder runs, then appends the whole reply at once. The
// accumulated user turns are the mission text persisted with the config.
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

// MissionMessage is one turn in the briefing dialog.
type MissionMessage struct {
	FromUser bool
	Text     string
}

// MissionResponder produces the copilot's reply to the briefing so far. The app
// injects one backed by the session provider; a nil responder disables live
// replies (the briefing is still saved).
type MissionResponder func(ctx context.Context, history []MissionMessage) (string, error)

// MissionReplyMsg carries a copilot reply (or error) back to the panel; the app
// routes it to AutopilotSelector.DeliverMissionReply.
type MissionReplyMsg struct {
	Text string
	Err  error
}

type missionDialog struct {
	log      []MissionMessage
	input    textarea.Model
	spinner  spinner.Model
	thinking bool
	status   string // transient notice/error under the composer
}

func newMissionDialog() missionDialog {
	ta := newChromelessTextarea()
	ta.Placeholder = "Brief the copilot: what should it get done this session?"
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{Frames: kit.StarSpinnerFrames, FPS: kit.StarSpinnerFPS}
	sp.Style = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
	return missionDialog{input: ta, spinner: sp}
}

// resetMission clears the dialog and seeds it from the persisted mission text so
// re-opening the panel shows the prior briefing as the first user turn.
func (p *AutopilotSelector) resetMission() {
	p.mission.log = nil
	p.mission.thinking = false
	p.mission.status = ""
	p.mission.input.Reset()
	if m := strings.TrimSpace(p.snap.Mission); m != "" {
		p.mission.log = []MissionMessage{{FromUser: true, Text: m}}
	}
}

// enterMission focuses and sizes the composer when the view opens.
func (p *AutopilotSelector) enterMission() {
	p.mission.input.SetWidth(p.innerWidth())
	p.mission.input.SetHeight(3)
	p.mission.input.Focus()
	p.mission.input.CursorEnd()
}

func (p *AutopilotSelector) handleMissionKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.commitMission()
		p.mission.input.Blur()
		p.view = apMenu
		return nil
	case "enter":
		return p.sendMission()
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

// sendMission records the user's turn, updates the persisted mission, and (when
// a responder is wired) kicks off the copilot reply behind a spinner.
func (p *AutopilotSelector) sendMission() tea.Cmd {
	text := strings.TrimSpace(p.mission.input.Value())
	if text == "" || p.mission.thinking {
		return nil
	}
	p.mission.log = append(p.mission.log, MissionMessage{FromUser: true, Text: text})
	p.mission.input.Reset()
	p.commitMission()

	if p.respond == nil {
		p.mission.status = "no copilot model available — briefing saved without a reply"
		return nil
	}
	p.mission.thinking = true
	p.mission.status = ""
	history := append([]MissionMessage(nil), p.mission.log...)
	respond := p.respond
	request := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		reply, err := respond(ctx, history)
		return MissionReplyMsg{Text: reply, Err: err}
	}
	return tea.Batch(p.mission.spinner.Tick, request)
}

// clearMission wipes the whole briefing — the accumulated turns and the composer
// — so the mission resets to empty instead of only ever growing; commitMission
// then persists the cleared ("") mission.
func (p *AutopilotSelector) clearMission() {
	p.mission.log = nil
	p.mission.thinking = false
	p.mission.input.Reset()
	p.mission.status = "mission cleared"
	p.commitMission()
}

// commitMission folds the user turns into the persisted mission directive.
func (p *AutopilotSelector) commitMission() {
	var parts []string
	for _, m := range p.mission.log {
		if m.FromUser {
			parts = append(parts, m.Text)
		}
	}
	p.snap.Mission = strings.Join(parts, "\n")
}

// DeliverMissionReply is called by the app when a MissionReplyMsg arrives.
func (p *AutopilotSelector) DeliverMissionReply(text string, err error) {
	p.mission.thinking = false
	if err != nil {
		p.mission.status = "copilot error: " + err.Error()
		return
	}
	if t := strings.TrimSpace(text); t != "" {
		p.mission.log = append(p.mission.log, MissionMessage{FromUser: false, Text: t})
	}
}

// Thinking reports whether the mission dialog is awaiting a reply — the app
// gates spinner ticks on this.
func (p *AutopilotSelector) Thinking() bool {
	return p.active && p.view == apMission && p.mission.thinking
}

// UpdateSpinner advances the mission spinner and returns its next tick.
func (p *AutopilotSelector) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.mission.spinner, cmd = p.mission.spinner.Update(msg)
	return cmd
}

func (p *AutopilotSelector) missionHint() string {
	return kit.HintLine(keycap("enter")+" send", keycap("alt+enter")+" newline", keycap("ctrl+r")+" clear", keycap("esc")+" back")
}

func (p *AutopilotSelector) renderMission(width int) string {
	body := lipgloss.NewStyle().Width(width)
	var b strings.Builder
	if len(p.mission.log) == 0 && !p.mission.thinking {
		b.WriteString(apDescStyle.Render("Tell the copilot what to accomplish; it confirms how it plans to drive."))
		b.WriteString("\n\n")
	}
	for _, m := range p.mission.log {
		if m.FromUser {
			b.WriteString(missionUserStyle.Render("you"))
		} else {
			b.WriteString(missionCopilotStyle.Render("⏵ copilot"))
		}
		b.WriteString("\n")
		b.WriteString(body.Render(m.Text))
		b.WriteString("\n\n")
	}
	if p.mission.thinking {
		b.WriteString(missionCopilotStyle.Render("⏵ copilot"))
		b.WriteString("\n")
		b.WriteString(p.mission.spinner.View() + " " + apDescStyle.Render("thinking…"))
		b.WriteString("\n\n")
	}
	b.WriteString(apRuleStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")
	b.WriteString(p.mission.input.View())
	if p.mission.status != "" {
		b.WriteString("\n")
		b.WriteString(apDescStyle.Render(p.mission.status))
	}
	return b.String()
}

var (
	missionUserStyle    = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Bold(true)
	missionCopilotStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent).Bold(true)
)
