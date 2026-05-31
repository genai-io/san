package input

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/setting"
)

// newConfigSelectorForTest builds an isolated ConfigSelector without a live
// settings backend so tests stay hermetic. Enter() initializes the snapshot
// from setting.SelfLearnSettings's zero value (= "feature off" baseline).
func newConfigSelectorForTest() *ConfigSelector {
	c := NewConfigSelector(nil)
	c.Enter(120, 40)
	return &c
}

// TestConfigSelectorCursorSkipsHeaders guards against the nil-toggle panic:
// the cursor must never land on a rowHeader / rowSpacer / rowAdvHint (their
// toggle is nil and Space would crash). Enter() should pick the first
// editable row; navigation should skip non-editable rows.
func TestConfigSelectorCursorSkipsHeaders(t *testing.T) {
	c := newConfigSelectorForTest()
	rows := c.rows()

	// Initial cursor must be on an editable row.
	if !rows[c.cursor].editable {
		t.Fatalf("initial cursor on non-editable row %d (%v)", c.cursor, rows[c.cursor].kind)
	}

	// Space must not panic regardless of which row is current.
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeySpace}) // toggle the first editable row

	// Walking ↑↓ across the whole panel must never land on a non-editable row.
	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
		if !rows[c.cursor].editable {
			t.Fatalf("down cursor landed on non-editable row %d (%v)", c.cursor, rows[c.cursor].kind)
		}
	}
	for range len(rows) * 2 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyUp})
		if !rows[c.cursor].editable {
			t.Fatalf("up cursor landed on non-editable row %d (%v)", c.cursor, rows[c.cursor].kind)
		}
	}
}

// TestConfigSelectorIsActivated checks that Enter flips the panel on and
// Esc flips it back off.
func TestConfigSelectorIsActivated(t *testing.T) {
	c := newConfigSelectorForTest()
	if !c.IsActive() {
		t.Fatal("Enter should activate the panel")
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEsc})
	if c.IsActive() {
		t.Fatal("Esc should deactivate")
	}
}

// TestConfigSelectorTogglesBool checks that Space and Enter on a bool row
// flip the underlying field.
func TestConfigSelectorTogglesBool(t *testing.T) {
	c := newConfigSelectorForTest()
	// Cursor starts on the first editable row, which is "Enable
	// memory-evolving" (the rowBool right after the Memory header).
	if c.snap.Memory.Enabled {
		t.Fatal("baseline: Memory.Enabled should be false")
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeySpace})
	if !c.snap.Memory.Enabled {
		t.Fatal("space should toggle Memory.Enabled true")
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter})
	if c.snap.Memory.Enabled {
		t.Fatal("enter should toggle Memory.Enabled false")
	}
}

// TestConfigSelectorIntEditAndClamp drives the int-edit flow: enter
// triggers editing mode, digits build up the buffer, the value clamps to
// the row's max on confirm.
func TestConfigSelectorIntEditAndClamp(t *testing.T) {
	c := newConfigSelectorForTest()
	// Cursor starts on Enable memory-evolving (the first editable row).
	// Navigate to "Max size (KB)": Enable → cadence → maxKB = 2 Downs.
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyDown})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter}) // enter edit mode
	if !c.editing {
		t.Fatal("Enter on int row should start editing")
	}
	// Clear buffer.
	for range 4 {
		c.HandleKeypress(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	// Type "99" — well above the 25 KB cap.
	c.HandleKeypress(tea.KeyMsg{Runes: []rune{'9'}})
	c.HandleKeypress(tea.KeyMsg{Runes: []rune{'9'}})
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if c.editing {
		t.Fatal("Enter should commit and exit edit mode")
	}
	if got := c.snap.Memory.MaxKB; got != setting.SelfLearnMaxMemoryKB {
		t.Fatalf("MaxKB clamped: got %d, want %d (max)", got, setting.SelfLearnMaxMemoryKB)
	}
}

// TestConfigSelectorRenderShowsValidationError confirms an illegal boolean
// combination (e.g. create=true, update=false) surfaces the §3.1 error
// message inline beneath the rows.
func TestConfigSelectorRenderShowsValidationError(t *testing.T) {
	c := newConfigSelectorForTest()
	// Force an illegal combo: create allowed (zero), update denied.
	c.snap.Skills.DenyUpdate = true
	out := c.Render()
	if !strings.Contains(out, "denyUpdate=true requires denyCreate=true") {
		t.Fatalf("Render should show §3.1 validation error, got:\n%s", out)
	}
}

// TestConfigSelectorTabFlipsScope toggles between user / project save
// targets.
func TestConfigSelectorTabFlipsScope(t *testing.T) {
	c := newConfigSelectorForTest()
	if c.scope != "user" {
		t.Fatalf("default scope: got %q, want user", c.scope)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyTab})
	if c.scope != "project" {
		t.Fatalf("after tab: got %q, want project", c.scope)
	}
	c.HandleKeypress(tea.KeyMsg{Type: tea.KeyTab})
	if c.scope != "user" {
		t.Fatalf("after second tab: got %q, want user", c.scope)
	}
}
