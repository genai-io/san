// /autopilot popup: configures the autopilot copilot — how it drives (system
// prompt), which lifecycle points it steers, and the mission it steers toward.
// It edits a working copy of setting.AutoPilotSettings; Save writes the
// autoPilot block to user settings, and Export/Import move it through a shared
// file for reuse across sessions and projects.
//
// The panel is a small state machine over three views: a menu (steer toggles +
// editor entries + Save/Export/Import), a full-screen System Prompt editor, and
// the Mission dialog (autopilot_mission.go). It renders its own centered frame.
package input

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/reviewer"
	"github.com/genai-io/san/internal/setting"
)

// autopilotView is which screen the panel is showing.
type autopilotView int

const (
	apMenu         autopilotView = iota // steer toggles + editor entries
	apSystemPrompt                      // full-screen system-prompt editor
	apMission                           // mission dialog (autopilot_mission.go)
	apExport                            // name-a-preset input (autopilot_presets.go)
	apImport                            // pick-a-preset list (autopilot_presets.go)
)

// AutopilotSavedMsg is emitted on Save carrying the edited config. The app
// applies it to the live session (m.env.AutoPilot) and persists it as the
// default seed for new sessions.
type AutopilotSavedMsg struct{ Config setting.AutoPilotSettings }

// AutopilotSelector is the /autopilot overlay.
type AutopilotSelector struct {
	respond MissionResponder                 // injected; nil disables live mission replies
	live    func() setting.AutoPilotSettings // injected; returns the live session config

	active bool
	width  int
	height int
	view   autopilotView

	// snap is the working buffer; Save writes it to disk. baseline is snap as
	// of Enter() so the panel can flag unsaved edits.
	snap     setting.AutoPilotSettings
	baseline setting.AutoPilotSettings

	cursor        int
	editing       bool // inline-editing the continuation cap
	editingBuffer string
	status        string // transient export/import notice under the menu

	actionCursor int      // menu: 0 = Export, 1 = Import on the actions row
	nameBuffer   string   // apExport: the preset name being typed
	presets      []string // apImport: available preset names
	importCursor int      // apImport: selection

	prompt  textarea.Model // System Prompt editor
	mission missionDialog  // Mission dialog state (autopilot_mission.go)
}

// NewAutopilotSelector builds the overlay. The live session config is supplied
// later via SetConfigSource.
func NewAutopilotSelector() AutopilotSelector {
	return AutopilotSelector{
		prompt:  newChromelessTextarea(),
		mission: newMissionDialog(),
	}
}

// SetMissionResponder wires the copilot's LLM reply function for the Mission
// dialog. Called by the app once its provider is available; a nil responder
// leaves the dialog usable for composing but without live replies.
func (p *AutopilotSelector) SetMissionResponder(fn MissionResponder) { p.respond = fn }

// SetConfigSource wires the getter for the live session config. The panel seeds
// its working buffer from it on Enter, so what you edit is the running session's
// autopilot (not a stale settings snapshot).
func (p *AutopilotSelector) SetConfigSource(fn func() setting.AutoPilotSettings) { p.live = fn }

// Enter activates the overlay on the menu view with a fresh working buffer.
func (p *AutopilotSelector) Enter(width, height int) {
	p.width = width
	p.height = height
	p.active = true
	p.view = apMenu
	p.editing = false
	p.editingBuffer = ""
	if p.live != nil {
		p.snap = p.live().Clone()
	}
	p.baseline = p.snap.Clone()
	p.cursor = p.firstSelectable()
	p.status = ""
	p.actionCursor = 0
	p.resetMission()
}

// IsActive implements overlayPanel.
func (p *AutopilotSelector) IsActive() bool { return p.active }

// Dirty reports unsaved edits (used by the header tag).
func (p *AutopilotSelector) Dirty() bool { return !p.snap.Equal(p.baseline) }

// HandleKeypress implements overlayPanel.
func (p *AutopilotSelector) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
	if !p.active {
		return nil
	}
	switch p.view {
	case apSystemPrompt:
		return p.handlePromptKey(msg)
	case apMission:
		return p.handleMissionKey(msg)
	case apExport:
		return p.handleExportKey(msg)
	case apImport:
		return p.handleImportKey(msg)
	default:
		return p.handleMenuKey(msg)
	}
}

// ── Menu view ───────────────────────────────────────────────────────────

func (p *AutopilotSelector) handleMenuKey(msg tea.KeyMsg) tea.Cmd {
	if p.editing {
		return p.handleEditingKey(msg)
	}
	rows := p.rows()
	if p.cursor >= len(rows) {
		p.cursor = p.firstSelectable()
	}
	switch msg.String() {
	case "esc":
		p.active = false
	case "up", "k":
		p.cursor = apStep(rows, p.cursor-1, -1, p.cursor)
	case "down", "j":
		p.cursor = apStep(rows, p.cursor+1, +1, p.cursor)
	case "left", "h":
		if rows[p.cursor].kind == apRowActions {
			p.actionCursor = 0
		}
	case "right", "l":
		if rows[p.cursor].kind == apRowActions {
			p.actionCursor = 1
		}
	case "space":
		// Space is a shortcut for the primary action of a toggle row only; on any
		// other row it's a no-op (enter opens those).
		if row := rows[p.cursor]; row.kind == apRowSteer {
			return p.activateRow(row)
		}
	case "enter":
		return p.activateRow(rows[p.cursor])
	}
	return nil
}

// activateRow performs the cursor row's primary action.
func (p *AutopilotSelector) activateRow(row apRow) tea.Cmd {
	switch row.kind {
	case apRowEntry:
		p.openView(row.open)
	case apRowSteer:
		row.toggle(&p.snap)
		p.reclampCursor()
	case apRowInt:
		p.editing = true
		p.editingBuffer = strconv.Itoa(p.snap.ResolvedMaxContinuations())
	case apRowActions:
		if p.actionCursor == 0 {
			p.beginExport()
		} else {
			p.beginImport()
		}
	case apRowSave:
		return p.save()
	}
	return nil
}

// openView switches to an editor sub-view, seeding it from the working buffer.
func (p *AutopilotSelector) openView(v autopilotView) {
	switch v {
	case apSystemPrompt:
		// Seed with the built-in system prompt when there's no override, so the
		// user sees and edits the real prompt rather than a blank box.
		seed := p.snap.SystemPrompt
		if seed == "" {
			seed = reviewer.DefaultSystemPrompt()
		}
		p.prompt.SetValue(seed)
		p.prompt.SetWidth(p.innerWidth())
		p.prompt.SetHeight(p.editorHeight())
		p.prompt.CursorEnd()
		p.prompt.Focus()
		p.view = apSystemPrompt
	case apMission:
		p.enterMission()
		p.view = apMission
	}
}

func (p *AutopilotSelector) handleEditingKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.editing = false
		p.editingBuffer = ""
	case "enter":
		if v, err := strconv.Atoi(p.editingBuffer); err == nil {
			if v < 1 {
				v = 1
			}
			if v > 999 {
				v = 999
			}
			p.snap.MaxContinuations = v
		}
		p.editing = false
		p.editingBuffer = ""
	case "backspace":
		if n := len(p.editingBuffer); n > 0 {
			p.editingBuffer = p.editingBuffer[:n-1]
		}
	default:
		if t := msg.Key().Text; len(t) == 1 && t[0] >= '0' && t[0] <= '9' && len(p.editingBuffer) < 3 {
			p.editingBuffer += t
		}
	}
	return nil
}

// save hands the working buffer to the app (which applies it to the live session
// and persists the default) and dismisses the popup.
func (p *AutopilotSelector) save() tea.Cmd {
	cfg := p.snap.Clone()
	p.active = false
	return func() tea.Msg { return AutopilotSavedMsg{Config: cfg} }
}

// ── System Prompt editor view ───────────────────────────────────────────

func (p *AutopilotSelector) handlePromptKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "esc" {
		val := strings.TrimRight(p.prompt.Value(), "\n")
		// Left as the built-in system prompt (unchanged) → store nothing, so the
		// panel keeps reading "built-in" and Dirty() doesn't flag a no-op edit.
		if strings.TrimSpace(val) == strings.TrimSpace(reviewer.DefaultSystemPrompt()) {
			val = ""
		}
		p.snap.SystemPrompt = val
		p.prompt.Blur()
		p.view = apMenu
		return nil
	}
	var cmd tea.Cmd
	p.prompt, cmd = p.prompt.Update(msg)
	return cmd
}

// ── Rows ────────────────────────────────────────────────────────────────

type apRowKind int

const (
	apRowEntry   apRowKind = iota // opens a sub-view (System Prompt / Mission)
	apRowSteer                    // bool toggle
	apRowInt                      // continuation cap
	apRowActions                  // Export | Import on one line (left/right picks)
	apRowSave                     // save action
	apRowSection                  // section header
	apRowSpacer                   // blank line
)

// apRow is one renderable menu row. Fields unused by the kind stay zero.
type apRow struct {
	kind    apRowKind
	label   string
	desc    string                                 // muted description after the label
	open    autopilotView                          // apRowEntry: view to switch to
	summary func(setting.AutoPilotSettings) string // apRowEntry: right-aligned value hint
	get     func(setting.AutoPilotSettings) bool   // apRowSteer: current state
	toggle  func(*setting.AutoPilotSettings)       // apRowSteer: flip it
	indent  int
}

func (r apRow) selectable() bool {
	switch r.kind {
	case apRowEntry, apRowSteer, apRowInt, apRowActions, apRowSave:
		return true
	default:
		return false
	}
}

func (p *AutopilotSelector) rows() []apRow {
	rows := []apRow{
		{kind: apRowEntry, label: "System Prompt", desc: "how it drives", open: apSystemPrompt, summary: systemPromptSummary},
		{kind: apRowSpacer},
		{kind: apRowSection, label: "Steer"},
		{kind: apRowSteer, label: "Turn Start", desc: "rewrite each input", get: getTurnStart, toggle: toggleTurnStart},
		{kind: apRowSteer, label: "Permission", desc: "auto-approve gray zone", get: getPermission, toggle: togglePermission},
		{kind: apRowSteer, label: "Bash", desc: "answer command prompts", get: getBash, toggle: toggleBash},
		{kind: apRowSteer, label: "Question", desc: "answer AskUserQuestion", get: getQuestion, toggle: toggleQuestion},
		{kind: apRowSteer, label: "Turn End", desc: "auto-continue the turn", get: getTurnEnd, toggle: toggleTurnEnd},
	}
	if p.snap.Steers.TurnEnd {
		rows = append(rows, apRow{kind: apRowInt, label: "Continue at most", indent: 1})
	}
	rows = append(rows,
		apRow{kind: apRowSpacer},
		apRow{kind: apRowSection, label: "Mission"},
		apRow{kind: apRowEntry, label: "Mission", desc: "what to achieve", open: apMission, summary: missionSummary},
		apRow{kind: apRowSpacer},
		apRow{kind: apRowActions},
		apRow{kind: apRowSpacer},
		apRow{kind: apRowSave, label: "Save"},
	)
	return rows
}

// reclampCursor keeps the cursor on a selectable row after a toggle changes the
// row set (Turn End reveals/hides the continuation cap).
func (p *AutopilotSelector) reclampCursor() {
	rows := p.rows()
	if p.cursor < len(rows) && rows[p.cursor].selectable() {
		return
	}
	p.cursor = apStep(rows, p.cursor, -1, p.firstSelectable())
}

func (p *AutopilotSelector) firstSelectable() int { return apStep(p.rows(), 0, +1, 0) }

// apStep walks rows from start in direction step until a selectable row,
// returning fallback if none is found that way.
func apStep(rows []apRow, start, step, fallback int) int {
	for i := start; i >= 0 && i < len(rows); i += step {
		if rows[i].selectable() {
			return i
		}
	}
	return fallback
}

// ── Steer accessors ─────────────────────────────────────────────────────

func getTurnStart(s setting.AutoPilotSettings) bool  { return s.Steers.TurnStart }
func getPermission(s setting.AutoPilotSettings) bool { return s.Steers.PermissionOn() }
func getBash(s setting.AutoPilotSettings) bool       { return s.Steers.BashPrompt }
func getQuestion(s setting.AutoPilotSettings) bool   { return s.Steers.Question }
func getTurnEnd(s setting.AutoPilotSettings) bool    { return s.Steers.TurnEnd }

func toggleTurnStart(s *setting.AutoPilotSettings) { s.Steers.TurnStart = !s.Steers.TurnStart }
func toggleBash(s *setting.AutoPilotSettings)      { s.Steers.BashPrompt = !s.Steers.BashPrompt }
func toggleQuestion(s *setting.AutoPilotSettings)  { s.Steers.Question = !s.Steers.Question }
func toggleTurnEnd(s *setting.AutoPilotSettings)   { s.Steers.TurnEnd = !s.Steers.TurnEnd }

// togglePermission flips the tri-state permission steer, writing an explicit
// value so an off-toggle persists distinctly from the default-on.
func togglePermission(s *setting.AutoPilotSettings) {
	on := !s.Steers.PermissionOn()
	s.Steers.Permission = &on
}

func systemPromptSummary(s setting.AutoPilotSettings) string {
	switch {
	case s.SystemPrompt != "":
		return "custom"
	case s.SystemPromptFile != "":
		return "file"
	default:
		return "built-in"
	}
}

func missionSummary(s setting.AutoPilotSettings) string {
	if strings.TrimSpace(s.Mission) == "" {
		return "empty"
	}
	return kit.TruncateText(strings.TrimSpace(s.Mission), 32)
}

// innerWidth is the card's content column — a generous fill of the terminal so
// the panel reads as a confident, roomy card, capped so rows don't sprawl on an
// ultra-wide screen. The -16 leaves room for the card's border + padding (6) and
// a screen margin.
func (p *AutopilotSelector) innerWidth() int   { return min(max(p.width-16, 56), 122) }
func (p *AutopilotSelector) editorHeight() int { return max(8, p.height-16) }
