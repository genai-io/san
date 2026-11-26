package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/persona"
)

func TestHandlePersonaCommand_Switch(t *testing.T) {
	var got string
	c := NewSlashCommandController(SlashCommandEnv{
		SetActivePersona: func(name string) error { got = name; return nil },
	})
	out, _, err := c.handlePersonaCommand(context.Background(), "  ml-researcher  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ml-researcher" {
		t.Errorf("SetActivePersona called with %q, want trimmed ml-researcher", got)
	}
	if !strings.Contains(out, "ml-researcher") {
		t.Errorf("output = %q, want it to name the persona", out)
	}
}

func TestHandlePersonaCommand_SwitchFailureSurfaced(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{
		SetActivePersona: func(string) error { return errors.New("boom") },
	})
	out, _, _ := c.handlePersonaCommand(context.Background(), "x")
	if !strings.Contains(out, "Failed") || !strings.Contains(out, "boom") {
		t.Errorf("output = %q, want the failure surfaced", out)
	}
}

func TestHandlePersonaCommand_Unavailable(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{}) // no SetActivePersona wired
	out, _, _ := c.handlePersonaCommand(context.Background(), "x")
	if !strings.Contains(out, "unavailable") {
		t.Errorf("output = %q, want an unavailable message", out)
	}
}

func TestHandlePersonaCommand_OpensPicker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := &Model{Persona: NewPersonaSelector(persona.NewRegistry(""), nil)}
	c := NewSlashCommandController(SlashCommandEnv{Input: m, Width: 80, Height: 24})
	out, _, err := c.handlePersonaCommand(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("bare /persona opens the picker, no text; got %q", out)
	}
	if !m.Persona.IsActive() {
		t.Error("bare /persona should activate the persona selector")
	}
}
