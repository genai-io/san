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
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing override %q\n---\n%s", want, p)
		}
	}
	assertPersonaRules(t, p, "Follow the custom rulebook.")
	// Identity and behavior defaults must be gone for the overridden parts.
	for _, gone := range []string{"You are a coding agent", "Be concise and direct"} {
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
	assertPersonaRules(t, p, "")
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

	if !strings.Contains(swapped, "Persona identity.") || strings.Contains(swapped, "You are a coding agent") {
		t.Error("swap did not replace the default identity")
	}
	assertPersonaRules(t, swapped, "Persona rules.")
	// Behavior was not overridden → still the default.
	if !strings.Contains(swapped, "Be concise and direct") {
		t.Error("un-overridden behavior should remain the default")
	}

	// A second persona replaces the previous persona rules, not the harness rules.
	SwapPersona(sys, Persona{Rules: "  Replacement rules.\n"})
	assertPersonaRules(t, sys.Prompt(), "Replacement rules.")
	if strings.Contains(sys.Prompt(), "Persona rules.") {
		t.Error("previous persona rules survived the swap")
	}

	// Revert: empty parts restore the exact built-in prompt.
	SwapPersona(sys, Persona{})
	if reverted := sys.Prompt(); reverted != def {
		t.Errorf("revert should restore the default prompt byte-for-byte")
	}
}

func assertPersonaRules(t *testing.T, prompt, personaRules string) {
	t.Helper()
	want := "<rules>\n" + cachedRules
	if personaRules != "" {
		want += "\n\n## Persona rules\n\n" + personaRules
	}
	want += "\n</rules>"
	if strings.Count(prompt, "<rules>") != 1 || !strings.Contains(prompt, want) {
		t.Errorf("want one rules section with intact harness rules followed by any persona rules:\n%s", prompt)
	}
}
