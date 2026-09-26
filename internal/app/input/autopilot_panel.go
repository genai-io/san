// /autopilot popup: configures the autopilot copilot on one page. GIVE IT holds
// what it works with (mission, system prompt, model), LET IT what it may do on
// the human's behalf, and PRESETS saves or loads the whole setup. It edits a
// working copy of setting.AutoPilotSettings, saved when esc closes the popup.
//
// The menu opens sub-views for the multi-step edits: the Mission dialog
// (autopilot_mission.go), the System prompt editor, the Model picker
// (autopilot_model.go) and the preset views (autopilot_presets.go).
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
	apMenu         autopilotView = iota // the one-page menu
	apSystemPrompt                      // full-screen system-prompt editor
	apMission                           // mission dialog (autopilot_mission.go)
	apExport                            // name-a-preset input (autopilot_presets.go)
	apImport                            // pick-a-preset list (autopilot_presets.go)
	apModel                             // judge-model picker (autopilot_model.go)
)

// AutopilotSavedMsg is emitted when the panel saves with edits, carrying the
// edited config. The app applies it to the live session (m.env.AutoPilot) and
// persists it as the default seed for new sessions.
type AutopilotSavedMsg struct{ Config setting.AutoPilotSettings }

// AutopilotSelector is the /autopilot overlay.
type AutopilotSelector struct {
	refine MissionRefiner                   // injected; nil disables mission refinement
	live   func() setting.AutoPilotSettings // injected; returns the live session config

	modelSource  func() []string           // injected; connected "vendor/model" refs
	effortSource func(ref string) []string // injected; a ref's reasoning rungs
	models       []string                  // apModel: refs loaded on open
	pickVendor   string                    // apModel: chosen provider; "" = provider list
	pickModel    string                    // apModel: chosen "vendor/model"; set = rung list
	efforts      []string                  // apModel: pickModel's reasoning rungs
	modelFilter  string                    // apModel: typed filter over pickVendor's models
	modelCursor  int                       // apModel: selection in modelChoices

	active bool
	width  int
	height int
	view   autopilotView

	// snap is the working buffer; esc saves it. baseline is snap as of
	// Enter() so closing an unedited panel saves nothing.
	snap     setting.AutoPilotSettings
	baseline setting.AutoPilotSettings

	cursor int
	status string // transient notice under the menu

	nameBuffer   string   // apExport: the preset name being typed
	presets      []string // saved preset names, listed on Enter
	importCursor int      // apImport: selection

	prompt  textarea.Model // System prompt editor
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

// SetMissionRefiner wires the copilot's LLM refine function for the Mission
// dialog. Called by the app once its provider is available; a nil refiner leaves
// the dialog usable for composing but without AI refinement.
func (p *AutopilotSelector) SetMissionRefiner(fn MissionRefiner) { p.refine = fn }

// SetConfigSource wires the getter for the live session config. The panel seeds
// its working buffer from it on Enter, so what you edit is the running session's
// autopilot (not a stale settings snapshot).
func (p *AutopilotSelector) SetConfigSource(fn func() setting.AutoPilotSettings) { p.live = fn }

// Enter activates the overlay on the menu view with a fresh working buffer.
func (p *AutopilotSelector) Enter(width, height int) {
	p.Resize(width, height)
	p.active = true
	p.view = apMenu
	if p.live != nil {
		p.snap = p.live().Clone()
	}
	p.baseline = p.snap.Clone()
	p.presets, _ = setting.ListAutoPilotPresets()
	p.cursor = p.firstSelectable()
	p.status = ""
	p.resetMission()
}

// Resize refreshes the dimensions used by the fullscreen frame and any open
// editor. Keeping the width captured by Enter after a terminal resize makes
// the terminal hard-wrap every stale-width row, which visually splices pieces
// of neighboring menu rows together.
func (p *AutopilotSelector) Resize(width, height int) {
	p.width = width
	p.height = height
	switch p.view {
	case apSystemPrompt:
		p.prompt.SetWidth(p.innerWidth())
		p.prompt.SetHeight(p.editorHeight())
	case apMission:
		p.mission.input.SetWidth(max(p.innerWidth()-missionRailWidth, 1))
		p.mission.input.SetHeight(missionEditorHeight)
	}
}

// IsActive implements overlayPanel.
func (p *AutopilotSelector) IsActive() bool { return p.active }

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
	case apModel:
		return p.handleModelKey(msg)
	default:
		return p.handleMenuKey(msg)
	}
}

// HandlePaste implements pasteHandler. Bracketed paste arrives as tea.PasteMsg,
// not a tea.KeyMsg, so it never reaches HandleKeypress — without this the app
// drops it (see pasteHandler in update.go) rather than let it leak into the
// prompt textarea behind the overlay. Route it into whichever text editor is
// focused; newlines are kept since a mission or system prompt is multi-line.
func (p *AutopilotSelector) HandlePaste(content string) tea.Cmd {
	if !p.active || content == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	switch p.view {
	case apMission:
		// Mid-refine the box is about to be replaced; don't let a paste race it.
		if p.mission.refining {
			return nil
		}
		p.mission.input.InsertString(content)
	case apSystemPrompt:
		p.prompt.InsertString(content)
	}
	return nil
}

// ── Menu view ───────────────────────────────────────────────────────────

func (p *AutopilotSelector) handleMenuKey(msg tea.KeyMsg) tea.Cmd {
	rows := p.rows()
	if p.cursor >= len(rows) {
		p.cursor = p.firstSelectable()
	}
	row := rows[p.cursor]
	switch msg.String() {
	case "esc":
		return p.save()
	case "up", "k":
		p.cursor = apStep(rows, p.cursor-1, -1, p.cursor)
	case "down", "j":
		p.cursor = apStep(rows, p.cursor+1, +1, p.cursor)
	case "left", "h":
		if row.adjust != nil {
			row.adjust(p, -1)
		}
	case "right", "l":
		if row.adjust != nil {
			row.adjust(p, +1)
		}
	case "space", "enter":
		if row.act != nil {
			row.act(p)
		}
	}
	return nil
}

// save dismisses the popup, handing any edits to the app (which applies them to
// the live session and persists the default). Launching is not the panel's job:
// entering AutoPilot offers to start a ready mission, and /goal starts one.
func (p *AutopilotSelector) save() tea.Cmd {
	p.active = false
	if p.snap.Equal(p.baseline) {
		return nil
	}
	cfg := p.snap.Clone()
	return func() tea.Msg { return AutopilotSavedMsg{Config: cfg} }
}

// openSystemPrompt opens the editor, seeded with the built-in instructions when
// there is no override so the user edits the real prompt, not a blank box.
func (p *AutopilotSelector) openSystemPrompt() {
	seed := p.snap.SystemPrompt
	if seed == "" {
		seed = reviewer.DefaultSteeringInstructions()
	}
	p.prompt.SetValue(seed)
	p.prompt.SetWidth(p.innerWidth())
	p.prompt.SetHeight(p.editorHeight())
	p.prompt.CursorEnd()
	p.prompt.Focus()
	p.view = apSystemPrompt
}

func (p *AutopilotSelector) openMission() {
	p.enterMission()
	p.view = apMission
}

// ── System prompt editor view ───────────────────────────────────────────

func (p *AutopilotSelector) handlePromptKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "esc" {
		val := strings.TrimRight(p.prompt.Value(), "\n")
		// Left as the built-in instructions (unchanged) → store nothing, so the
		// panel keeps reading "built-in" and Dirty() doesn't flag a no-op edit.
		if strings.TrimSpace(val) == strings.TrimSpace(reviewer.DefaultSteeringInstructions()) {
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
	apRowEntry   apRowKind = iota // opens a sub-view; value shown right-aligned
	apRowToggle                   // checkbox
	apRowSection                  // section header
	apRowSpacer                   // blank line
)

// apRow is one renderable menu row. Fields unused by the kind stay zero.
type apRow struct {
	kind     apRowKind
	label    string
	desc     string                               // muted description after the label
	value    string                               // apRowEntry: right-aligned current value
	on       bool                                 // apRowToggle: checked
	disabled bool                                 // drawn dim; act explains why instead
	act      func(*AutopilotSelector)             // enter/space
	adjust   func(p *AutopilotSelector, step int) // ←/→, when the row has a value to step
}

func (r apRow) selectable() bool { return r.kind == apRowEntry || r.kind == apRowToggle }

func (p *AutopilotSelector) rows() []apRow {
	s := p.snap
	var continueRow apRow
	if strings.TrimSpace(s.Mission) != "" {
		continueRow = apRow{
			kind: apRowToggle, label: "Continue", on: s.Steers.TurnEnd,
			desc:   "to the next turn · " + continueLimit(s),
			act:    func(p *AutopilotSelector) { p.snap.Steers.TurnEnd = !p.snap.Steers.TurnEnd },
			adjust: stepContinuations,
		}
	} else {
		continueRow = apRow{
			kind: apRowToggle, label: "Continue", disabled: true,
			desc: "to the next turn · needs a mission",
			act:  func(p *AutopilotSelector) { p.status = "write a mission first — Continue drives toward it" },
		}
	}

	loadDesc := strconv.Itoa(len(p.presets)) + " saved"
	if p.baseline.MissionState == setting.MissionRunning {
		loadDesc += " · replaces the running mission"
	}

	return []apRow{
		{kind: apRowSection, label: "Give it"},
		{kind: apRowEntry, label: "Mission", desc: "the goal it works toward", value: p.missionValue(),
			act: (*AutopilotSelector).openMission},
		{kind: apRowEntry, label: "System", desc: "how it thinks and decides (system prompt)", value: systemPromptValue(s),
			act: (*AutopilotSelector).openSystemPrompt},
		{kind: apRowEntry, label: "Model", desc: "which model makes the calls", value: modelValue(s),
			act: (*AutopilotSelector).beginModelPick},
		{kind: apRowSpacer},
		{kind: apRowSection, label: "Let it"},
		{kind: apRowToggle, label: "Suggest", desc: "your next input · Tab to accept", on: s.Steers.SuggestOn(),
			act: func(p *AutopilotSelector) { flip(&p.snap.Steers.Suggest, p.snap.Steers.SuggestOn()) }},
		{kind: apRowToggle, label: "Approve", desc: "permission requests · asks you if risky", on: s.Steers.PermissionOn(),
			act: func(p *AutopilotSelector) { flip(&p.snap.Steers.Permission, p.snap.Steers.PermissionOn()) }},
		{kind: apRowToggle, label: "Answer", desc: "questions and [y/N] prompts", on: answerOn(s),
			act: func(p *AutopilotSelector) {
				on := !answerOn(p.snap)
				p.snap.Steers.Question, p.snap.Steers.BashPrompt = on, on
			}},
		continueRow,
		{kind: apRowSpacer},
		{kind: apRowSection, label: "Presets"},
		{kind: apRowEntry, label: "Save preset…", desc: "reuse this setup in other sessions",
			act: (*AutopilotSelector).beginExport},
		{kind: apRowEntry, label: "Load preset…", desc: loadDesc,
			act: (*AutopilotSelector).beginImport},
	}
}

// flip toggles a tri-state default-on steer, writing an explicit value so an
// off persists distinctly from the default.
func flip(p **bool, current bool) {
	v := !current
	*p = &v
}

// answerOn reports the Answer toggle, which drives both answering steers: the
// agent's questions and a running command's prompts. Either one on reads as on
// so a legacy config with just one set still shows it.
func answerOn(s setting.AutoPilotSettings) bool { return s.Steers.Question || s.Steers.BashPrompt }

// continuationLadder is what ←/→ steps the Continue cap through; -1 is no limit.
var continuationLadder = []int{5, 10, 20, 50, 100, setting.AutoPilotUnlimitedContinuations}

// stepContinuations moves the Continue cap one rung along continuationLadder. A
// hand-set value off the ladder steps to its nearest neighbor.
func stepContinuations(p *AutopilotSelector, step int) {
	// rank orders no-limit after every finite cap.
	rank := func(v int) int {
		if v < 0 {
			return 1 << 30
		}
		return v
	}
	cur := rank(p.snap.MaxContinuations)
	if p.snap.MaxContinuations == 0 {
		cur = setting.AutoPilotDefaultMaxContinuations
	}
	l := continuationLadder
	if step > 0 {
		for _, v := range l {
			if rank(v) > cur {
				p.snap.MaxContinuations = v
				return
			}
		}
		return
	}
	for i := len(l) - 1; i >= 0; i-- {
		if rank(l[i]) < cur {
			p.snap.MaxContinuations = l[i]
			return
		}
	}
}

func continueLimit(s setting.AutoPilotSettings) string {
	if s.ContinuationsUnlimited() {
		return "no limit"
	}
	return "up to " + strconv.Itoa(s.ResolvedMaxContinuations())
}

// missionValue is the Mission row's value: the text and where its run stands,
// as of opening the panel. An edited mission reads "ready" because saving it
// starts a fresh run.
func (p *AutopilotSelector) missionValue() string {
	mission := strings.TrimSpace(p.snap.Mission)
	if mission == "" {
		return "not set · Enter to write"
	}
	label := "ready"
	if mission == strings.TrimSpace(p.baseline.Mission) {
		switch p.baseline.MissionState {
		case setting.MissionDone:
			label = "✓ done"
		case "":
		default:
			label = string(p.baseline.MissionState)
		}
	}
	return kit.TruncateText(mission, 32) + " · " + label
}

func systemPromptValue(s setting.AutoPilotSettings) string {
	switch {
	case s.SystemPrompt != "":
		return "custom"
	case s.SystemPromptFile != "":
		return "file"
	default:
		return "built-in"
	}
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

// innerWidth is the card's content column — a generous fill of the terminal so
// the panel reads as a confident, roomy card, capped so rows don't sprawl on an
// ultra-wide screen. The -16 leaves room for the card's border + padding (6) and
// a screen margin.
func (p *AutopilotSelector) innerWidth() int   { return min(max(p.width-16, 1), 122) }
func (p *AutopilotSelector) editorHeight() int { return max(8, p.height-16) }
