// The Auto Skill tab of /reflect edits review settings and learned skills.
//
// The panel is a small mode machine layered over the config form:
//
//	modeForm     the config zone (skillConfigForm) — edit + save the settings.
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

	"github.com/genai-io/san/internal/autoskill"
	"github.com/genai-io/san/internal/setting"
)

// LearnedSkillStore is the app-injected accessor bundle backing the inventory:
// List the agent-created skills, Read one's full SKILL.md for preview, Delete
// one from disk. Any func may be nil (e.g. in tests), in which case that
// capability is inert.
type LearnedSkillStore struct {
	List   func() []autoskill.SkillInfo
	Read   func(name string) (string, error)
	Delete func(name string) error
}

// learnedItem is one row of the LEARNED drill-in — an agent-created skill.
type learnedItem struct {
	name     string // skill name, also the Read+Delete key
	subtitle string // skill scope
	desc     string // skill description
}

type skillMode int

const (
	modeForm skillMode = iota
	modeList
	modeView
	modeConfirm
	modeStrategy
)

type autoSkillPanel struct {
	form     skillConfigForm
	store    LearnedSkillStore
	strategy strategyEditor

	mode      skillMode
	items     []learnedItem // loaded at Enter / drill-in
	invCursor int
	actionErr error // last delete/preview failure, surfaced inline; cleared on nav

	// view (modeView) sub-state.
	viewName   string
	viewLines  []string
	viewOffset int
}

func newAutoSkillPanel(workspace func() (string, *setting.Settings), store LearnedSkillStore) *autoSkillPanel {
	p := &autoSkillPanel{
		store:    store,
		strategy: newStrategyEditor(autoskill.DefaultStrategy()),
	}
	p.form = skillConfigForm{
		workspace: workspace,
		scope:     setting.ScopeUser,
		rows:      p.configRows(),
	}
	return p
}

func (p *autoSkillPanel) Title() string { return "Auto Skill" }
func (p *autoSkillPanel) Dirty() bool   { return p.form.Dirty() }
func (p *autoSkillPanel) HintLine() string {
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
func (p *autoSkillPanel) Modal() bool { return p.mode != modeForm }

// Breadcrumb names the open sub-view for the header lockup.
func (p *autoSkillPanel) Breadcrumb() string {
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

func (p *autoSkillPanel) Enter() {
	p.form.Enter()
	p.mode = modeForm
	p.invCursor = 0
	p.viewOffset = 0
	p.actionErr = nil
	p.reloadItems()
}

// reloadItems re-reads the on-disk agent-created skills and keeps the cursor
// in range.
func (p *autoSkillPanel) reloadItems() {
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
}

func (p *autoSkillPanel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
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
func (p *autoSkillPanel) handleFormKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	cmd, done, activated := p.form.HandleKey(msg)
	switch activated {
	case "strategy":
		p.openStrategy()
	case "learned":
		p.openLearned()
	}
	return cmd, done
}

func (p *autoSkillPanel) skillStatus() string {
	if p.form.workspace == nil {
		return "settings unavailable"
	}
	_, svc := p.form.workspace()
	if svc == nil {
		return "settings unavailable"
	}
	if setting.AutoSkillDisabledByEnv() {
		return "off · SAN_DISABLE_SELF_LEARN"
	}
	cfg := svc.AutoSkill()
	if cfg.Validate() != nil {
		return "off · invalid skill settings"
	}
	if !cfg.Skills.Active() {
		return "off · no skill permissions"
	}
	if !svc.ReviewSkillEnabled() {
		return "off · ReviewSkill disabled"
	}
	return "on"
}

// openLearned drills into the learned-skills inventory sub-view.
func (p *autoSkillPanel) openLearned() {
	p.reloadItems()
	p.invCursor = 0
	p.actionErr = nil
	p.mode = modeList
}

// openStrategy seeds the Strategy editor from the working snapshot and
// enters the modal sub-view.
func (p *autoSkillPanel) openStrategy() {
	p.strategy.open(p.form.snap.Strategy)
	p.mode = modeStrategy
}

// handleStrategyKey drives the Strategy editor: Enter saves the edit back to
// the snapshot and returns; Alt/Shift+Enter inserts a newline; Esc discards
// and returns; everything else feeds the textarea.
func (p *autoSkillPanel) handleStrategyKey(msg tea.KeyMsg) tea.Cmd {
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
func (p *autoSkillPanel) HandlePaste(content string) {
	if p.mode == modeStrategy {
		p.strategy.insert(content)
	}
}

func (p *autoSkillPanel) handleListKey(msg tea.KeyMsg) tea.Cmd {
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

func (p *autoSkillPanel) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
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
func (p *autoSkillPanel) deleteSelected() {
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

func (p *autoSkillPanel) openView() {
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

func (p *autoSkillPanel) handleViewKey(msg tea.KeyMsg) tea.Cmd {
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

// configRows builds the fixed layout; summaries read their current values.
func (p *autoSkillPanel) configRows() []configRow {
	skillPerm := func(label string, get func(setting.AutoSkillPermissions) bool, flip func(*setting.AutoSkillPermissions)) configRow {
		return configRow{
			kind:       rowBool,
			label:      label,
			indent:     2,
			boolGetter: func(s *setting.AutoSkillSettings) bool { return get(s.Skills) },
			toggle:     func(s *setting.AutoSkillSettings) { flip(&s.Skills) },
		}
	}
	return []configRow{
		{kind: rowSubHeader, label: "Auto Skill", indent: 1, summary: p.skillStatus},
		{
			kind:    rowEntry,
			entryID: "strategy",
			label:   "Strategy",
			desc:    "how to guide the learning",
			indent:  1,
			summary: func() string { return strategySummary(p.form.snap.Strategy) },
		},
		skillPerm("Create new skills", setting.AutoSkillPermissions.AllowCreate,
			func(s *setting.AutoSkillPermissions) { s.DenyCreate = !s.DenyCreate }),
		skillPerm("Update a skill", setting.AutoSkillPermissions.AllowUpdate,
			func(s *setting.AutoSkillPermissions) { s.DenyUpdate = !s.DenyUpdate }),
		skillPerm("Delete a skill", setting.AutoSkillPermissions.AllowDelete,
			func(s *setting.AutoSkillPermissions) { s.DenyDelete = !s.DenyDelete }),
		{kind: rowSpacer},
		{
			kind:    rowEntry,
			entryID: "learned",
			label:   "Learned",
			desc:    "skills — browse & prune",
			indent:  1,
			summary: func() string {
				if len(p.items) == 0 {
					return "none yet"
				}
				return strconv.Itoa(len(p.items))
			},
		},
		{kind: rowSpacer},
		{kind: rowSave, label: "Save"},
	}
}
