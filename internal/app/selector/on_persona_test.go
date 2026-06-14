package selector

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/persona"
)

// writeUserPersona scaffolds a minimal user-scope persona under $HOME for tests.
func writeUserPersona(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, ".san", "personas", name, "system")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.md"), []byte("You are "+name+"."), 0o644); err != nil {
		t.Fatal(err)
	}
}

func selectByName(s *Persona, name string) bool {
	for i, it := range s.items {
		if it.Name == name {
			s.selectedIdx = i
			return true
		}
	}
	return false
}

func TestPersonaSelector_EnterMarksCurrentAndSelects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewPersona(persona.NewRegistry(""), nil) // only the built-in default

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
	s := NewPersona(persona.NewRegistry(""), nil)
	_ = s.EnterSelect(80, 24)
	s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if s.IsActive() {
		t.Error("Esc should cancel the selector")
	}
}

func TestPersonaSelector_DeleteFlowEmitsMsg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUserPersona(t, home, "tester")

	s := NewPersona(persona.NewRegistry(""), nil)
	_ = s.EnterSelect(80, 24)
	if !selectByName(&s, "tester") {
		t.Fatal("tester persona should be listed")
	}

	// Ctrl+D only arms the confirm; the delete fires on "y".
	s.HandleKeypress(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if !s.confirmDelete {
		t.Fatal("Ctrl+D should arm the delete confirmation")
	}
	// A non-y key backs out without deleting.
	s.HandleKeypress(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if s.confirmDelete {
		t.Fatal("a non-y key should cancel the confirmation")
	}

	s.HandleKeypress(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	cmd := s.HandleKeypress(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y should fire the delete")
	}
	msg, ok := cmd().(PersonaDeleteMsg)
	if !ok || msg.Name != "tester" {
		t.Fatalf("got %#v, want PersonaDeleteMsg{tester}", cmd())
	}
	if s.IsActive() {
		t.Error("picker should close after confirming delete")
	}
}

func TestPersonaSelector_OpenEmitsMsg(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeUserPersona(t, home, "tester")

	s := NewPersona(persona.NewRegistry(""), nil)
	_ = s.EnterSelect(80, 24)
	if !selectByName(&s, "tester") {
		t.Fatal("tester persona should be listed")
	}
	cmd := s.HandleKeypress(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+O should emit an open message")
	}
	if msg, ok := cmd().(PersonaOpenMsg); !ok || msg.Name != "tester" {
		t.Fatalf("got %#v, want PersonaOpenMsg{tester}", cmd())
	}
	if s.IsActive() {
		t.Error("picker should close after opening")
	}
}

func TestPersonaSelector_NoActionsOnBuiltin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewPersona(persona.NewRegistry(""), nil) // only the built-in default
	_ = s.EnterSelect(80, 24)
	if cmd := s.HandleKeypress(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}); cmd != nil || s.confirmDelete {
		t.Error("Ctrl+D on the built-in default should be a no-op")
	}
	if cmd := s.HandleKeypress(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}); cmd != nil {
		t.Error("Ctrl+O on the built-in default should be a no-op")
	}
}

func TestUpdatePersona_AppliesAndCancels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var got string
	deps := Runtime{
		State:            &State{},
		SetActivePersona: func(name string) error { got = name; return nil },
	}
	s := NewPersona(persona.NewRegistry(""), nil)
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
