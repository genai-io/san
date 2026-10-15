// The /evolve panel: configures skill self-learning permissions and manages
// the skills the reviewer has learned.
//
// The panel is a small mode machine layered over the config form:
//
//	modeForm     the config zone (selfLearnForm) — edit + save the settings.
//	modeStrategy the Strategy editor — a full-text learning-strategy override.
//	modeList     the LEARNED inventory — agent-created skills; esc returns to
//	             the form.
//	modeView     a full, scrollable read-only SKILL.md preview.
//	modeConfirm  a "delete <name>?" y/n prompt.
//
// Every mode except modeForm is modal (see modalPanel): while in one the panel
// captures the shell's esc / Tab so those keys drive the sub-view rather than
// dismissing the popup or switching tabs.
//
// Config edits go through the form's user/project Save; inventory deletes are
// immediate, human-initiated actions (not the reviewer's gated writes) and
// take effect on disk at once.
package input

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/selflearn"
	"github.com/genai-io/san/internal/setting"
)

// EvolveDeps bundles everything the /evolve panel needs. All fields are
// optional; a zero value builds a usable (inert-inventory, zero-settings)
// popup for tests.
type EvolveDeps struct {
	// Workspace returns the live cwd + settings service. The form and the
	// learned stores read through it so a project reload is picked up.
	Workspace func() (string, *setting.Settings)
	Learned   LearnedSkillStore
	// Recent returns the rolling self-learning activity log for the RECENT
	// zone of the Learned drill-in.
	Recent func() []LearnEvent
}

// NewEvolveSelector builds the /evolve popup: the skill self-learning panel.
func NewEvolveSelector(d EvolveDeps) PanelPopup {
	return newPanelPopup("✦", "Self-Learning", "learns as you work",
		newEvolvePanel(d))
}

// LearnedSkillStore is the app-injected accessor bundle backing the inventory:
// List the agent-created skills, Read one's full SKILL.md for preview, Delete
// one from disk. Any func may be nil (e.g. in tests), in which case that
// capability is inert.
type LearnedSkillStore struct {
	List   func() []selflearn.SkillInfo
	Read   func(name string) (string, error)
	Delete func(name string) error
}

// learnedItem is one row of the LEARNED drill-in — an agent-created skill.
type learnedItem struct {
	name     string // skill name, also the Read+Delete key
	subtitle string // skill scope
	desc     string // skill description
}

// LearnEvent is one row of the RECENT self-learning activity recap: a write the
// reviewer made, humanized for display. Sourced from the app's rolling activity
// log.
type LearnEvent struct {
	Verb   string // "created" | "patched" | "removed" | …
	Target string // skill name
	Note   string // one-line "what changed"
	Ago    string // humanized age, e.g. "2m ago"
}

type skillMode int

const (
	modeForm skillMode = iota
	modeList
	modeView
	modeConfirm
	modeStrategy
)

type evolvePanel struct {
	form   selfLearnForm
	store  LearnedSkillStore
	recent func() []LearnEvent // nil = no RECENT zone

	strategy strategyEditor

	mode  skillMode
	items []learnedItem // loaded at Enter / drill-in
	// rows is the cached config-row list; rebuilt only when items changes
	// (its one input), so per-keypress/per-frame rowsFn calls don't rebuild
	// ~20 closures each time.
	rows      []configRow
	events    []LearnEvent // RECENT snapshot, loaded at Enter
	invCursor int
	actionErr error // last delete/preview failure, surfaced inline; cleared on nav

	// view (modeView) sub-state.
	viewName   string
	viewLines  []string
	viewOffset int
}

func newEvolvePanel(d EvolveDeps) *evolvePanel {
	p := &evolvePanel{
		store:    d.Learned,
		recent:   d.Recent,
		strategy: newStrategyEditor(selflearn.DefaultStrategy()),
		rows:     evolveRows(0),
	}
	p.form = selfLearnForm{
		workspace: d.Workspace,
		scope:     setting.ScopeUser,
		rowsFn:    func() []configRow { return p.rows },
	}
	return p
}

func (p *evolvePanel) Title() string { return "self-learning" }
func (p *evolvePanel) Dirty() bool   { return p.form.Dirty() }
func (p *evolvePanel) HintLine() string {
	switch p.mode {
	case modeStrategy:
		return keycap("enter") + " save  " + keycap("esc") + " discard  " + keycap("alt+enter") + " newline"
	case modeView:
		return keycap("↑↓") + " scroll  " + keycap("esc") + " back"
	case modeConfirm:
		return keycap("y") + " delete  " + keycap("n") + " cancel"
	case modeList:
		return keycap("↑↓") + " navigate  " + keycap("enter") + " view  " +
			keycap("d") + " delete  " + keycap("esc") + " back"
	default:
		return p.form.HintLine()
	}
}

// Modal reports the sub-views that capture esc / Tab from the shell — every
// mode except the base config form.
func (p *evolvePanel) Modal() bool { return p.mode != modeForm }

// Breadcrumb names the open sub-view for the header lockup.
func (p *evolvePanel) Breadcrumb() string {
	switch p.mode {
	case modeStrategy:
		return "strategy"
	case modeList, modeConfirm:
		return "learned"
	case modeView:
		return "preview"
	default:
		return ""
	}
}

func (p *evolvePanel) Enter() {
	p.form.Enter()
	p.mode = modeForm
	p.invCursor = 0
	p.viewOffset = 0
	p.actionErr = nil
	p.reloadItems()
	p.reloadRecent()
}

// reloadItems re-reads the on-disk agent-created skills and keeps the cursor
// in range.
func (p *evolvePanel) reloadItems() {
	p.items = nil
	if p.store.List != nil {
		for _, s := range p.store.List() {
			p.items = append(p.items, learnedItem{
				name: s.Name, subtitle: s.Level, desc: s.Description,
			})
		}
	}
	if p.invCursor >= len(p.items) {
		p.invCursor = max(0, len(p.items)-1)
	}
	// items is the row list's only input, so this is the one rebuild point.
	p.rows = evolveRows(len(p.items))
}

// reloadRecent snapshots the recent activity log.
func (p *evolvePanel) reloadRecent() {
	if p.recent == nil {
		p.events = nil
		return
	}
	p.events = p.recent()
}

func (p *evolvePanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch p.mode {
	case modeStrategy:
		return p.handleStrategyKey(msg), false
	case modeView:
		return p.handleViewKey(msg), false
	case modeConfirm:
		return p.handleConfirmKey(msg), false
	case modeList:
		return p.handleListKey(msg), false
	default:
		return p.handleFormKey(msg)
	}
}

// handleFormKey delegates to the config form, then opens whichever entry row
// the user activated: the Strategy editor or the Learned inventory.
func (p *evolvePanel) handleFormKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	cmd, done, activated := p.form.HandleKey(msg)
	switch activated {
	case "strategy":
		p.openStrategy()
	case "learned":
		p.openLearned()
	}
	return cmd, done
}

// openLearned drills into the learned-skills inventory sub-view.
func (p *evolvePanel) openLearned() {
	p.reloadItems()
	p.reloadRecent()
	p.invCursor = 0
	p.actionErr = nil
	p.mode = modeList
}

// openStrategy seeds the Strategy editor from the working snapshot and
// enters the modal sub-view.
func (p *evolvePanel) openStrategy() {
	p.strategy.open(p.form.snap.Strategy)
	p.mode = modeStrategy
}

// handleStrategyKey drives the Strategy editor: Enter saves the edit back to
// the snapshot and returns; Alt/Shift+Enter inserts a newline; Esc discards
// and returns; everything else feeds the textarea.
func (p *evolvePanel) handleStrategyKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.mode = modeForm // discard — leave the working config unchanged
		return nil
	case "enter":
		p.form.snap.Strategy = p.strategy.value()
		p.mode = modeForm
		return nil
	case "alt+enter", "shift+enter":
		p.strategy.insertNewline()
		return nil
	default:
		return p.strategy.handleKey(msg)
	}
}

// HandlePaste feeds bracketed paste into the Strategy editor.
func (p *evolvePanel) HandlePaste(content string) {
	if p.mode == modeStrategy {
		p.strategy.insert(content)
	}
}

func (p *evolvePanel) handleListKey(msg tea.KeyMsg) tea.Cmd {
	p.actionErr = nil
	switch msg.String() {
	case "esc":
		p.mode = modeForm // back out of the inventory to the config
	case "up", "k":
		if p.invCursor > 0 {
			p.invCursor--
		}
	case "down", "j":
		if p.invCursor < len(p.items)-1 {
			p.invCursor++
		}
	case "enter", "v", "right":
		p.openView()
	case "d":
		if len(p.items) > 0 {
			p.mode = modeConfirm
		}
	}
	return nil
}

func (p *evolvePanel) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "enter":
		p.deleteSelected()
	case "n", "esc":
		p.mode = modeList
	}
	return nil
}

// deleteSelected removes the focused skill from disk,
// reloads the inventory, and returns to the list (or the form when it empties).
func (p *evolvePanel) deleteSelected() {
	if p.invCursor >= len(p.items) {
		p.mode = modeList
		return
	}
	it := p.items[p.invCursor]
	if del := p.store.Delete; del != nil {
		if err := del(it.name); err != nil {
			p.actionErr = err
			p.mode = modeList
			return
		}
	}
	p.reloadItems()
	if len(p.items) == 0 {
		p.mode = modeForm
	} else {
		p.mode = modeList
	}
}

func (p *evolvePanel) openView() {
	if len(p.items) == 0 {
		return
	}
	it := p.items[p.invCursor]
	read := p.store.Read
	p.viewName = it.name
	p.viewOffset = 0
	p.actionErr = nil
	p.viewLines = nil
	if read == nil {
		p.actionErr = errPreviewUnavailable
		return // stay in list; the inline error explains why
	}
	content, err := read(it.name)
	if err != nil {
		p.actionErr = err
		return
	}
	p.viewLines = splitLines(content)
	p.mode = modeView
}

func (p *evolvePanel) handleViewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "backspace", "left":
		p.mode = modeList
	case "up", "k":
		p.viewOffset--
	case "down", "j":
		p.viewOffset++
	case "pgup":
		p.viewOffset -= viewPageStep
	case "pgdown", " ":
		p.viewOffset += viewPageStep
	case "home", "g":
		p.viewOffset = 0
	case "end", "G":
		p.viewOffset = len(p.viewLines) // clamped to max in renderView
	}
	if p.viewOffset < 0 {
		p.viewOffset = 0
	}
	return nil
}

// evolveRows is the /evolve config zone. Triggering is model-decided (the
// Evolve tool), so there are no cadence knobs: the skill checkboxes are pure
// permission gates on what a model-requested review may do to the skill set,
// and there is no separate enable toggle — skill-evolving is on when at least
// one action is allowed. learnedCount is shown on the "Learned" entry that
// drills into the inventory.
func evolveRows(learnedCount int) []configRow {
	skillPerm := func(label string, get func(setting.SelfLearnSkills) bool, flip func(*setting.SelfLearnSkills)) configRow {
		return configRow{
			kind:       rowBool,
			label:      label,
			indent:     2,
			boolGetter: func(s *setting.SelfLearnSettings) bool { return get(s.Skills) },
			toggle:     func(s *setting.SelfLearnSettings) { flip(&s.Skills) },
		}
	}
	return []configRow{
		{
			kind:    rowEntry,
			entryID: "strategy",
			label:   "Strategy",
			desc:    "how to guide the learning",
			indent:  1,
			summary: func(s *setting.SelfLearnSettings) string { return strategySummary(s.Strategy) },
		},
		{kind: rowSpacer},
		{kind: rowSubHeader, label: "Skills", indent: 1},
		skillPerm("Create new skills", setting.SelfLearnSkills.AllowCreate,
			func(s *setting.SelfLearnSkills) { s.DenyCreate = !s.DenyCreate }),
		skillPerm("Update a skill", setting.SelfLearnSkills.AllowUpdate,
			func(s *setting.SelfLearnSkills) { s.DenyUpdate = !s.DenyUpdate }),
		skillPerm("Delete a skill", setting.SelfLearnSkills.AllowDelete,
			func(s *setting.SelfLearnSkills) { s.DenyDelete = !s.DenyDelete }),
		{kind: rowSpacer},
		{
			kind:    rowEntry,
			entryID: "learned",
			label:   "Learned",
			desc:    "skills — browse & prune",
			indent:  1,
			summary: func(*setting.SelfLearnSettings) string {
				if learnedCount == 0 {
					return "none yet"
				}
				return strconv.Itoa(learnedCount)
			},
		},
		{kind: rowSpacer},
		{kind: rowSave, label: "Save"},
	}
}
