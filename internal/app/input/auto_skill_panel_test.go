package input

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/autoskill"
)

func newTestSkillPanel() (*PanelPopup, *autoSkillPanel) {
	return newTestSkillPanelWith(LearnedSkillStore{})
}

// TestSkillPanelCursorSkipsHeaders guards against nil-toggle panics: the
// cursor must never land on a section/sub-header / spacer row (toggle is nil →
// Space would crash).
func TestSkillPanelCursorSkipsHeaders(t *testing.T) {
	c, p := newTestSkillPanel()
	rows := p.form.rows

	if !rows[p.form.cursor].editable() {
		t.Fatalf("initial cursor on non-editable row %d (%v)", p.form.cursor, rows[p.form.cursor].kind)
	}

	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace}) // toggle the first editable row

	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
		if !rows[p.form.cursor].editable() {
			t.Fatalf("down landed on non-editable row %d (%v)", p.form.cursor, rows[p.form.cursor].kind)
		}
	}
	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
		if !rows[p.form.cursor].editable() {
			t.Fatalf("up landed on non-editable row %d (%v)", p.form.cursor, rows[p.form.cursor].kind)
		}
	}
}

func TestReflectionOpensAndCloses(t *testing.T) {
	c, _ := newTestSkillPanel()
	if !c.IsActive() {
		t.Fatal("Enter should activate the popup")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if c.IsActive() {
		t.Fatal("Esc should deactivate")
	}
}

// TestSkillPanelTogglesBool walks the checkbox flow on Space. The first editable
// row is the Strategy entry, so step down onto the "Create new skills"
// permission (a plain checkbox — triggering is model-decided, no cadence).
func TestSkillPanelTogglesBool(t *testing.T) {
	c, p := newTestSkillPanel()
	if !p.form.snap.Skills.AllowCreate() {
		t.Fatal("baseline: create should be allowed (Deny* zero value)")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown}) // Strategy → Create
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace})
	if p.form.snap.Skills.AllowCreate() {
		t.Fatal("space should deny create")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace})
	if !p.form.snap.Skills.AllowCreate() {
		t.Fatal("space should re-allow create")
	}
}

// TestSkillPanelRenderShowsValidationError confirms an invalid combination
// (create allowed but update denied) surfaces the inline error.
func TestSkillPanelRenderShowsValidationError(t *testing.T) {
	c, p := newTestSkillPanel()
	p.form.snap.Skills.DenyUpdate = true
	out := c.Render()
	if !strings.Contains(out, `"Create new skills" needs "Update a skill"`) {
		t.Fatalf("Render should surface the validation error, got:\n%s", out)
	}
}

func TestReflectionTabsPreserveDraftUntilClose(t *testing.T) {
	c := NewReflectionSelector(ReflectionDeps{})
	c.Enter(120, 40)
	press := func(key rune) { c.HandleKeypress(tea.KeyPressMsg{Code: key}) }
	if out := c.Render(); !strings.Contains(out, "Reflection") || !strings.Contains(out, "Browse files and stores with /memory") || strings.Contains(out, "Create new skills") {
		t.Fatalf("expected only the Auto Memory body:\n%s", out)
	}
	press(tea.KeyTab)
	if out := c.Render(); !strings.Contains(out, "Create new skills") || strings.Contains(out, "Browse files and stores") {
		t.Fatalf("expected only the Auto Skill body:\n%s", out)
	}
	press(tea.KeyDown) // Strategy → Create
	press(tea.KeySpace)
	press(tea.KeyTab)
	press(tea.KeyTab)
	if !c.ActivePanel().Dirty() {
		t.Fatal("switching tabs discarded the pending skill changes")
	}
	press(tea.KeyEscape)
	c.Enter(120, 40)
	if c.ActivePanel().Title() != "Auto Skill" || c.ActivePanel().Dirty() {
		t.Fatal("reopening should remember the tab, not the discarded changes")
	}
}

// TestSkillPanelScopeFlips toggles between user / project save targets with ←→.
func TestSkillPanelScopeFlips(t *testing.T) {
	c, p := newTestSkillPanel()
	if p.form.scope != "user" {
		t.Fatalf("default scope: got %q, want user", p.form.scope)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyRight})
	if p.form.scope != "project" {
		t.Fatalf("after →: got %q", p.form.scope)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyLeft})
	if p.form.scope != "user" {
		t.Fatalf("after ←: got %q", p.form.scope)
	}
}

// TestSkillPanelStrategyEditorFlow opens the Strategy editor (seeded with the
// built-in autoskill.DefaultStrategy), confirms it is modal, and that an
// unchanged default collapses to "" while an edited override persists (into
// the shared Strategy setting).
func TestSkillPanelStrategyEditorFlow(t *testing.T) {
	c, p := newTestSkillPanel()

	// Strategy is the first editable row, so enter opens it.
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.mode != modeStrategy || !p.Modal() {
		t.Fatalf("enter on Strategy should open a modal editor, mode=%d", p.mode)
	}
	if p.Breadcrumb() != "strategy" {
		t.Fatalf("breadcrumb = %q", p.Breadcrumb())
	}

	// Enter with the seed unchanged from the built-in default saves nothing.
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.mode != modeForm {
		t.Fatal("enter should save the edit and return to the form")
	}
	if p.form.snap.Strategy != "" {
		t.Fatalf("unchanged default should collapse to empty, got %q", p.form.snap.Strategy)
	}

	// Reopen, type, Enter → a custom override is persisted.
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	c.HandleKeypress(tea.KeyPressMsg{Code: '!', Text: "!"})
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.form.snap.Strategy == "" {
		t.Fatal("edited strategy should persist a custom override on enter")
	}
	if !p.form.Dirty() {
		t.Fatal("a custom strategy override should mark the form dirty")
	}

	// Esc discards: reopen, type, esc → the saved value is unchanged.
	saved := p.form.snap.Strategy
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	c.HandleKeypress(tea.KeyPressMsg{Code: 'X', Text: "X"})
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.form.snap.Strategy != saved {
		t.Fatalf("esc should discard the edit; got %q, want %q", p.form.snap.Strategy, saved)
	}
	if p.mode != modeForm {
		t.Fatal("esc should return to the form")
	}
}

// ── Phase 2: learned-skills inventory ────────────────────────────────────

// fakeLearnedStore is a stateful in-memory LearnedSkillStore for tests: List
// reflects deletions, Read returns a multi-line stub SKILL.md.
func fakeLearnedStore(names ...string) LearnedSkillStore {
	skills := make([]autoskill.SkillInfo, len(names))
	for i, n := range names {
		skills[i] = autoskill.SkillInfo{Name: n, Level: "user", Description: n + " does a thing"}
	}
	return LearnedSkillStore{
		List: func() []autoskill.SkillInfo { return append([]autoskill.SkillInfo(nil), skills...) },
		Read: func(name string) (string, error) {
			return "---\nname: " + name + "\n---\n\nline one\nline two\nline three\n", nil
		},
		Delete: func(name string) error {
			for i, s := range skills {
				if s.Name == name {
					skills = append(skills[:i], skills[i+1:]...)
					return nil
				}
			}
			return errors.New("no such skill")
		},
	}
}

func newTestSkillPanelWith(store LearnedSkillStore) (*PanelPopup, *autoSkillPanel) {
	c := NewReflectionSelector(ReflectionDeps{Learned: store})
	c.Enter(120, 40)
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	return &c, c.ActivePanel().(*autoSkillPanel)
}

// openLearnedView navigates from the config to the "Learned skills" entry and
// opens the inventory drill-in (modeList).
func openLearnedView(c *PanelPopup, p *autoSkillPanel) {
	for range 20 {
		if p.mode != modeForm {
			return
		}
		if p.form.rows[p.form.cursor].entryID == "learned" {
			c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
			return
		}
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

// TestSkillPanelOpensInventory confirms the "Learned skills" entry drills into
// the inventory list, ↑↓ navigate it, and esc returns to the config form.
func TestSkillPanelOpensInventory(t *testing.T) {
	c, p := newTestSkillPanelWith(fakeLearnedStore("go-table-tests", "pr-triage"))
	openLearnedView(c, p)
	if p.mode != modeList || p.invCursor != 0 {
		t.Fatalf("Learned skills entry should open the list at 0, got mode %d cursor %d", p.mode, p.invCursor)
	}
	if !p.Modal() {
		t.Fatal("the inventory drill-in should be modal")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	if p.invCursor != 1 {
		t.Fatalf("down in list should advance cursor, got %d", p.invCursor)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.mode != modeForm {
		t.Fatalf("esc should return to the config form, got mode %d", p.mode)
	}
	if !c.IsActive() {
		t.Fatal("esc in the modal inventory must not dismiss the popup")
	}
}

// TestSkillPanelDeleteFlow drives d → confirm → y and checks the skill is
// removed from disk (the store) and the inventory reloads.
func TestSkillPanelDeleteFlow(t *testing.T) {
	c, p := newTestSkillPanelWith(fakeLearnedStore("go-table-tests", "pr-triage"))
	openLearnedView(c, p)
	c.HandleKeypress(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if p.mode != modeConfirm {
		t.Fatalf("d should open the delete confirm, got mode %d", p.mode)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if p.mode != modeList {
		t.Fatalf("y should delete and return to list, got mode %d", p.mode)
	}
	if len(p.items) != 1 || p.items[0].name != "pr-triage" {
		t.Fatalf("delete should leave only pr-triage, got %+v", p.items)
	}
	if p.actionErr != nil {
		t.Fatalf("unexpected action error: %v", p.actionErr)
	}
}

// TestSkillPanelViewScrollAndBack opens the preview, confirms it captures esc
// (modal) to return to the list rather than dismissing the popup.
func TestSkillPanelViewScrollAndBack(t *testing.T) {
	c, p := newTestSkillPanelWith(fakeLearnedStore("go-table-tests"))
	openLearnedView(c, p)
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter}) // open preview
	if p.mode != modeView {
		t.Fatalf("enter should open the preview, got mode %d", p.mode)
	}
	if len(p.viewLines) == 0 {
		t.Fatal("preview should have loaded the SKILL.md lines")
	}
	if !p.Modal() {
		t.Fatal("the preview should be modal")
	}
	// Esc is captured by the modal panel, so the popup stays open and returns
	// to the list rather than dismissing.
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !c.IsActive() {
		t.Fatal("esc in the modal preview must not dismiss the popup")
	}
	if p.mode != modeList {
		t.Fatalf("esc should return the preview to the list, got mode %d", p.mode)
	}
}
