package system

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestWithPersona_OverridesEachPart(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithPersona(Persona{
			Identity: "You are a custom persona.",
			Behavior: "Speak in haiku.",
			Rules:    "Follow the custom rulebook.",
		}),
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)
	p := sys.Prompt()

	for _, want := range []string{
		"You are a custom persona.",
		"<behavior>\nSpeak in haiku.\n</behavior>",
		"<rules>\nFollow the custom rulebook.\n</rules>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing override %q\n---\n%s", want, p)
		}
	}
	// Built-in defaults must be gone for the overridden parts.
	for _, gone := range []string{"You are a coding agent", "Be concise and direct", "## Safety"} {
		if strings.Contains(p, gone) {
			t.Errorf("prompt still contains default %q after override", gone)
		}
	}
}

func TestWithPersona_EmptyFieldsKeepDefaults(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithPersona(Persona{Behavior: "Only behavior overridden."}),
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)
	p := sys.Prompt()

	if !strings.Contains(p, "Only behavior overridden.") {
		t.Error("behavior override missing")
	}
	if strings.Contains(p, "Be concise and direct") {
		t.Error("default behavior should be replaced by the override")
	}
	// Identity and rules keep their built-in defaults.
	if !strings.Contains(p, "You are a coding agent") {
		t.Error("identity should keep its default")
	}
	if !strings.Contains(p, "## Safety") {
		t.Error("rules should keep their default")
	}
}

func TestSwapPersona_HotSwapAndRevert(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)
	def := sys.Prompt()
	if !strings.Contains(def, "You are a coding agent") || !strings.Contains(def, "## Safety") {
		t.Fatal("baseline prompt is missing defaults")
	}

	SwapPersona(sys, Persona{
		Identity: "Persona identity.",
		Rules:    "Persona rules.",
	})
	swapped := sys.Prompt()

	if !strings.Contains(swapped, "Persona identity.") || !strings.Contains(swapped, "Persona rules.") {
		t.Error("swap did not apply the overrides")
	}
	if strings.Contains(swapped, "You are a coding agent") || strings.Contains(swapped, "## Safety") {
		t.Error("defaults should be replaced after the swap")
	}
	// Behavior was not overridden → still the default.
	if !strings.Contains(swapped, "Be concise and direct") {
		t.Error("un-overridden behavior should remain the default")
	}

	// Revert: empty parts restore the exact built-in prompt.
	SwapPersona(sys, Persona{})
	if reverted := sys.Prompt(); reverted != def {
		t.Errorf("revert should restore the default prompt byte-for-byte")
	}
}
