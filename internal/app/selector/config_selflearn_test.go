package selector

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/setting"
)

// newTestPopup builds an isolated Config + active panel for tests.
// settings=nil so Enter() seeds the snapshot to the zero "feature off"
// baseline without touching disk.
func newTestPopup() (*Config, *selfLearnPanel) {
	c := NewConfig(nil)
	c.Enter(120, 40)
	return &c, c.ActivePanel().(*selfLearnPanel)
}

// TestSelfLearnPanelCursorSkipsHeaders guards against nil-toggle panics:
// the cursor must never land on a section/sub-header / spacer / hint row
// (toggle is nil → Space would crash).
func TestSelfLearnPanelCursorSkipsHeaders(t *testing.T) {
	c, p := newTestPopup()
	rows := p.rows()

	if !rows[p.cursor].editable() {
		t.Fatalf("initial cursor on non-editable row %d (%v)", p.cursor, rows[p.cursor].kind)
	}

	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace}) // toggle the first editable row

	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
		if !rows[p.cursor].editable() {
			t.Fatalf("down landed on non-editable row %d (%v)", p.cursor, rows[p.cursor].kind)
		}
	}
	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
		if !rows[p.cursor].editable() {
			t.Fatalf("up landed on non-editable row %d (%v)", p.cursor, rows[p.cursor].kind)
		}
	}
}

// TestConfigSelectorActivatesAndDismisses confirms the popup flips on with
// Enter() and off when Esc is delivered through HandleKeypress.
func TestConfigSelectorActivatesAndDismisses(t *testing.T) {
	c, _ := newTestPopup()
	if !c.IsActive() {
		t.Fatal("Enter should activate the popup")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if c.IsActive() {
		t.Fatal("Esc should deactivate")
	}
}

// TestSelfLearnPanelTogglesBool walks the bool-row flow on Space and Enter.
func TestSelfLearnPanelTogglesBool(t *testing.T) {
	c, p := newTestPopup()
	// Cursor lands on the first editable row, "Enable memory-evolving".
	if p.snap.Memory.Enabled {
		t.Fatal("baseline: Memory.Enabled should be false")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace})
	if !p.snap.Memory.Enabled {
		t.Fatal("space should toggle Memory.Enabled true")
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.snap.Memory.Enabled {
		t.Fatal("enter should toggle Memory.Enabled false")
	}
}

// TestSelfLearnPanelIntEditAndClamp drives the int-edit flow: Enter starts
// editing, digits build the buffer, the value clamps to the row's [min,max]
// on commit.
func TestSelfLearnPanelIntEditAndClamp(t *testing.T) {
	c, p := newTestPopup()
	// Cursor starts on Enable memory-evolving. Down twice to "Max size".
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter}) // start edit
	if !p.editing {
		t.Fatal("Enter on int row should start editing")
	}
	for range 4 {
		c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: '9', Text: "9"})
	c.HandleKeypress(tea.KeyPressMsg{Code: '9', Text: "9"})
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter}) // commit
	if p.editing {
		t.Fatal("Enter should commit and exit edit mode")
	}
	if got := p.snap.Memory.MaxKB; got != setting.SelfLearnMaxMemoryKB {
		t.Fatalf("MaxKB clamped: got %d, want %d", got, setting.SelfLearnMaxMemoryKB)
	}
}

// TestConfigSelectorRenderShowsValidationError confirms a §3.1 invalid
// combination surfaces the inline error.
func TestConfigSelectorRenderShowsValidationError(t *testing.T) {
	c, p := newTestPopup()
	p.snap.Skills.DenyUpdate = true
	out := c.Render()
	if !strings.Contains(out, `"Create new skills" needs "Update existing skills"`) {
		t.Fatalf("Render should surface the §3.1 error, got:\n%s", out)
	}
}

// TestConfigSelectorRenderShowsTabs confirms the "/config" header plus a tab
// strip naming every registered panel makes it into the popup output. (With a
// single panel the header is a breadcrumb instead; today two panels —
// self-learning and appearance — are registered, so it renders tabs.)
func TestConfigSelectorRenderShowsTabs(t *testing.T) {
	c, _ := newTestPopup()
	out := c.Render()
	for _, want := range []string{"/config", "self-learning", "appearance"} {
		if !strings.Contains(out, want) {
			t.Fatalf("header missing %q from render:\n%s", want, out)
		}
	}
}

// TestSelfLearnPanelTabFlipsScope toggles between user / project save targets.
func TestSelfLearnPanelTabFlipsScope(t *testing.T) {
	c, p := newTestPopup()
	if p.scope != "user" {
		t.Fatalf("default scope: got %q, want user", p.scope)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.scope != "project" {
		t.Fatalf("after tab: got %q", p.scope)
	}
	c.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.scope != "user" {
		t.Fatalf("after second tab: got %q", p.scope)
	}
}
