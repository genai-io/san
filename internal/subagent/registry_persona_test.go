package subagent

import (
	"strings"
	"testing"
)

// TestLoadPersona_AllowListRestrictsVisibility checks that a persona allow-list
// hides every agent not on it (spawn gate + agents directory), is
// case-insensitive, and is fully reversible.
func TestLoadPersona_AllowListRestrictsVisibility(t *testing.T) {
	r := NewRegistry()
	r.Register(&AgentConfig{Name: "reviewer", Description: "Reviews changes"})
	r.Register(&AgentConfig{Name: "implementer", Description: "Implements changes"})

	if !r.IsEnabled("reviewer") || !r.IsEnabled("implementer") {
		t.Fatal("custom agents should be enabled with no persona allow-list")
	}

	// One-agent allow-list: only it stays visible.
	r.LoadPersona([]string{"reviewer"})
	if !r.IsEnabled("reviewer") {
		t.Error("an allowed agent should stay enabled")
	}
	if r.IsEnabled("implementer") {
		t.Error("agents off the allow-list should be hidden")
	}
	section := r.GetAgentsSection()
	if !strings.Contains(section, "reviewer") {
		t.Error("agents directory should list the allowed agent")
	}
	if strings.Contains(section, "implementer") {
		t.Error("agents directory should omit non-allowed agents")
	}
	configs := r.ListConfigs()
	if len(configs) != 1 || configs[0].Name != "reviewer" {
		t.Fatalf("selector configs = %#v, want only reviewer", configs)
	}

	// The implicit default is not a registered custom agent and remains
	// available when a persona restricts the custom catalog.
	old := Default()
	SetDefaultRegistry(r)
	t.Cleanup(func() { SetDefaultRegistry(old) })
	if config, ok := resolveAgentConfig(""); !ok || config.Name != defaultAgentName {
		t.Fatalf("implicit default config = %#v, %v", config, ok)
	}

	// Case-insensitive + whitespace-trimmed.
	r.LoadPersona([]string{" Reviewer "})
	if !r.IsEnabled("reviewer") {
		t.Error("allow-list match should be case-insensitive and trimmed")
	}

	// A blank/empty list is treated as no restriction.
	r.LoadPersona([]string{"", "  "})
	if !r.IsEnabled("implementer") {
		t.Error("a blank allow-list should impose no restriction")
	}

	// ClearPersona restores everything.
	r.LoadPersona([]string{"reviewer"})
	r.ClearPersona()
	if !r.IsEnabled("reviewer") || !r.IsEnabled("implementer") {
		t.Error("ClearPersona should make all custom agents visible again")
	}
}
