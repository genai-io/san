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
		WithGitGuidelines(true),
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
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
	for _, gone := range []string{"interactive AI assistant", "## Tone", "## Safety", "## Git safety"} {
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
	if strings.Contains(p, "## Tone") {
		t.Error("default behavior should be replaced by the override")
	}
	// Identity and rules keep their built-in defaults.
	if !strings.Contains(p, "interactive AI assistant") {
		t.Error("identity should keep its default")
	}
	if !strings.Contains(p, "## Safety") {
		t.Error("rules should keep their default")
	}
}

func TestSwapPersona_HotSwapAndRevert(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithGitGuidelines(true),
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
	)
	def := sys.Prompt()
	if !strings.Contains(def, "interactive AI assistant") || !strings.Contains(def, "## Safety") {
		t.Fatal("baseline prompt is missing defaults")
	}

	SwapPersona(sys, Persona{
		Identity: "Persona identity.",
		Rules:    "Persona rules.",
	}, true, "")
	swapped := sys.Prompt()

	if !strings.Contains(swapped, "Persona identity.") || !strings.Contains(swapped, "Persona rules.") {
		t.Error("swap did not apply the overrides")
	}
	if strings.Contains(swapped, "interactive AI assistant") || strings.Contains(swapped, "## Safety") {
		t.Error("defaults should be replaced after the swap")
	}
	// Behavior was not overridden → still the default.
	if !strings.Contains(swapped, "## Tone") {
		t.Error("un-overridden behavior should remain the default")
	}

	// Revert: empty parts restore the exact built-in prompt.
	SwapPersona(sys, Persona{}, true, "")
	if reverted := sys.Prompt(); reverted != def {
		t.Errorf("revert should restore the default prompt byte-for-byte")
	}
}
