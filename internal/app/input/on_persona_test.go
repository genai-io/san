package input

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/persona"
)

func TestPersonaSelector_EnterMarksCurrentAndSelects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewPersonaSelector(persona.NewRegistry(""), nil) // only the built-in default

	if err := s.EnterSelect(80, 24); err != nil {
		t.Fatal(err)
	}
	if !s.IsActive() {
		t.Fatal("selector should be active after EnterSelect")
	}
	if len(s.items) == 0 {
		t.Fatal("expected at least the default persona")
	}
	// No settings.persona → current resolves to the built-in default, preselected.
	if !s.items[s.selectedIdx].IsCurrent || s.items[s.selectedIdx].Name != persona.DefaultName {
		t.Errorf("initial selection = %+v, want the current default", s.items[s.selectedIdx])
	}

	cmd := s.Select()
	if cmd == nil {
		t.Fatal("Select should return a command")
	}
	sel, ok := cmd().(PersonaSelectedMsg)
	if !ok {
		t.Fatalf("expected PersonaSelectedMsg, got %T", cmd())
	}
	if sel.Name != persona.DefaultName {
		t.Errorf("selected = %q, want %q", sel.Name, persona.DefaultName)
	}
}

func TestPersonaSelector_EscCancels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewPersonaSelector(persona.NewRegistry(""), nil)
	_ = s.EnterSelect(80, 24)
	s.HandleKeypress(tea.KeyMsg{Type: tea.KeyEsc})
	if s.IsActive() {
		t.Error("Esc should cancel the selector")
	}
}

func TestUpdatePersona_AppliesAndCancels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var got string
	deps := OverlayDeps{
		State:            &Model{},
		SetActivePersona: func(name string) error { got = name; return nil },
	}
	s := NewPersonaSelector(persona.NewRegistry(""), nil)
	_ = s.EnterSelect(80, 24)

	cmd, handled := UpdatePersona(deps, &s, PersonaSelectedMsg{Name: "ml-researcher"})
	if !handled {
		t.Fatal("UpdatePersona should handle PersonaSelectedMsg")
	}
	if got != "ml-researcher" {
		t.Errorf("SetActivePersona called with %q, want ml-researcher", got)
	}
	if s.IsActive() {
		t.Error("selector should be cancelled after applying")
	}
	if cmd == nil {
		t.Error("expected a status-timer command")
	}
}
