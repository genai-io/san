package input

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/setting"
)

// newTestPopup builds an isolated ConfigSelector + active panel for tests.
// settings=nil so Enter() seeds the snapshot to the zero "feature off"
// baseline without touching disk.
func newTestPopup() (*ConfigSelector, *selfLearnPanel) {
	c := NewConfigSelector(nil)
	c.Enter(120, 40)
	return &c, c.ActivePanel().(*selfLearnPanel)
}

// TestSelfLearnPanelCursorSkipsHeaders guards against nil-toggle panics:
// the cursor must never land on a section/sub-header / spacer / hint row
// (toggle is nil → Space would crash).
func TestSelfLearnPanelCursorSkipsHeaders(t *testing.T) {
	c, p := newTestPopup()
	rows := p.rows()

	if !rows[p.cursor].editable {
		t.Fatalf("initial cursor on non-editable row %d (%v)", p.cursor, rows[p.cursor].kind)
	}

	c.HandleKeypress(tea.KeyMsg{Type: tea.KeySpace}) // toggle the first editable row

	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
		if !rows[p.cursor].editable {
			t.Fatalf("down landed on non-editable row %d (%v)", p.cursor, rows[p.cursor].kind)
		}
	}
	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyUp})
		if !rows[p.cursor].editable {
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
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEsc})
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
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeySpace})
	if !p.snap.Memory.Enabled {
		t.Fatal("space should toggle Memory.Enabled true")
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter})
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
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter}) // start edit
	if !p.editing {
		t.Fatal("Enter on int row should start editing")
	}
	for range 4 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	c.HandleKeypress(tea.KeyMsg{Runes: []rune{'9'}})
	c.HandleKeypress(tea.KeyMsg{Runes: []rune{'9'}})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter}) // commit
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
	if !strings.Contains(out, "denyUpdate=true requires denyCreate=true") {
		t.Fatalf("Render should surface the §3.1 error, got:\n%s", out)
	}
}

// TestConfigSelectorRenderShowsBreadcrumb confirms the new "/config ›
// Self-Learning" breadcrumb makes it into the popup output.
func TestConfigSelectorRenderShowsBreadcrumb(t *testing.T) {
	c, _ := newTestPopup()
	out := c.Render()
	if !strings.Contains(out, "/config") || !strings.Contains(out, "self-learning") {
		t.Fatalf("breadcrumb missing from render:\n%s", out)
	}
}

// TestSelfLearnPanelTabFlipsScope toggles between user / project save targets.
func TestSelfLearnPanelTabFlipsScope(t *testing.T) {
	c, p := newTestPopup()
	if p.scope != "user" {
		t.Fatalf("default scope: got %q, want user", p.scope)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyTab})
	if p.scope != "project" {
		t.Fatalf("after tab: got %q", p.scope)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyTab})
	if p.scope != "user" {
		t.Fatalf("after second tab: got %q", p.scope)
	}
}
