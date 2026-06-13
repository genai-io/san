package subagent

import (
	"strings"
	"testing"
)

// TestLoadPersona_AllowListRestrictsVisibility checks that a persona allow-list
// hides every agent not on it (spawn gate + agents directory), is
// case-insensitive, and is fully reversible.
func TestLoadPersona_AllowListRestrictsVisibility(t *testing.T) {
	r := NewRegistry() // built-ins: general-purpose, code-simplifier, code-reviewer

	if !r.IsEnabled("general-purpose") || !r.IsEnabled("code-reviewer") {
		t.Fatal("built-ins should be enabled with no persona allow-list")
	}

	// One-agent allow-list: only it stays visible.
	r.LoadPersona([]string{"code-reviewer"})
	if !r.IsEnabled("code-reviewer") {
		t.Error("an allowed agent should stay enabled")
	}
	if r.IsEnabled("general-purpose") || r.IsEnabled("code-simplifier") {
		t.Error("agents off the allow-list should be hidden")
	}
	section := r.GetAgentsSection()
	if !strings.Contains(section, "code-reviewer") {
		t.Error("agents directory should list the allowed agent")
	}
	if strings.Contains(section, "general-purpose") {
		t.Error("agents directory should omit non-allowed agents")
	}

	// Case-insensitive + whitespace-trimmed.
	r.LoadPersona([]string{" Code-Reviewer "})
	if !r.IsEnabled("code-reviewer") {
		t.Error("allow-list match should be case-insensitive and trimmed")
	}

	// A blank/empty list is treated as no restriction.
	r.LoadPersona([]string{"", "  "})
	if !r.IsEnabled("general-purpose") {
		t.Error("a blank allow-list should impose no restriction")
	}

	// ClearPersona restores everything.
	r.LoadPersona([]string{"code-reviewer"})
	r.ClearPersona()
	if !r.IsEnabled("general-purpose") || !r.IsEnabled("code-simplifier") {
		t.Error("ClearPersona should make all agents visible again")
	}
}
