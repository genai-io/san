package setting

import (
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestPermissionDecisionProvenance(t *testing.T) {
	tests := []struct {
		name, tool string
		args       map[string]any
		rules      PermissionSettings
		session    *SessionPermissions
		want       PermissionDecision
	}{
		{name: "deny outranks bypass and grant", tool: "Read", rules: PermissionSettings{Deny: []string{"Read(**)"}}, session: &SessionPermissions{Mode: ModeBypassPermissions, AllowedTools: map[string]bool{"Read": true}}, want: PermissionDecision{Behavior: perm.Reject, By: "config", Reason: "deny rule: Read(**)"}},
		{name: "ask outranks grant", tool: "Read", rules: PermissionSettings{Ask: []string{"Read(**)"}}, session: &SessionPermissions{AllowedTools: map[string]bool{"Read": true}}, want: PermissionDecision{Behavior: perm.Prompt, By: "config", Reason: "ask rule: Read(**)"}},
		{name: "allow rule", tool: "Read", rules: PermissionSettings{Allow: []string{"Read(**)"}}, want: PermissionDecision{Behavior: perm.Permit, By: "config", Reason: "allow rule: Read(**)"}},
		{name: "tool grant outranks allow rule", tool: "Read", rules: PermissionSettings{Allow: []string{"Read(**)"}}, session: &SessionPermissions{AllowedTools: map[string]bool{"Read": true}}, want: PermissionDecision{Behavior: perm.Permit, By: "grant", Scope: "session", Reason: "session: allow all Read"}},
		{name: "pattern grant", tool: "Read", session: &SessionPermissions{AllowedPatterns: map[string]bool{"Read(**)": true}}, want: PermissionDecision{Behavior: perm.Permit, By: "grant", Scope: "session", Reason: "session pattern: Read(**)"}},
		{name: "bypass", tool: "Write", session: &SessionPermissions{Mode: ModeBypassPermissions}, want: PermissionDecision{Behavior: perm.Permit, By: "mode", Reason: "mode: bypass permissions"}},
		{name: "safe tool", tool: "Read", want: PermissionDecision{Behavior: perm.Permit, By: "mode", Reason: "mode: safe tool"}},
		{name: "read only shell", tool: "Bash", args: map[string]any{"command": "git status"}, session: &SessionPermissions{Mode: ModeReadOnly}, want: PermissionDecision{Behavior: perm.Permit, By: "mode", Reason: "mode: read-only bash command"}},
		{name: "confirmation outranks grant", tool: "Bash", args: map[string]any{"command": "git reset --hard"}, session: &SessionPermissions{Mode: ModeAutoPilot, AllowedTools: map[string]bool{"Bash": true}}, want: PermissionDecision{Behavior: perm.Prompt, By: "config", Reason: "confirmation: git command that discards work", Judgeable: true}},
		{name: "circuit breaker survives bypass", tool: "Bash", args: map[string]any{"command": "rm -rf /"}, session: &SessionPermissions{Mode: ModeBypassPermissions}, want: PermissionDecision{Behavior: perm.Prompt, By: "config", Reason: "circuit breaker: removal targets the filesystem root or home directory"}},
		{name: "dont ask replaces ask authority", tool: "Read", rules: PermissionSettings{Ask: []string{"Read(**)"}}, session: &SessionPermissions{Mode: ModeDontAsk}, want: PermissionDecision{Behavior: perm.Reject, By: "mode", Reason: "mode: don't ask (auto-deny): ask rule: Read(**)"}},
		{name: "headless replaces confirmation authority and clears judgeable", tool: "Bash", args: map[string]any{"command": "git reset --hard"}, session: &SessionPermissions{Mode: ModeAutoPilot, ShouldAvoidPrompts: true}, want: PermissionDecision{Behavior: perm.Reject, By: "mode", Reason: "headless: confirmation: git command that discards work"}},
		{name: "headless leaves deny authority intact", tool: "Read", rules: PermissionSettings{Deny: []string{"Read(**)"}}, session: &SessionPermissions{ShouldAvoidPrompts: true}, want: PermissionDecision{Behavior: perm.Reject, By: "config", Reason: "deny rule: Read(**)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Data{Permissions: tt.rules}
			if got := s.HasPermissionToUseTool(tt.tool, tt.args, tt.session); got != tt.want {
				t.Errorf("decision = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestModeDefaultProvenance(t *testing.T) {
	for _, mode := range []OperationMode{ModeNormal, ModeAutoAccept, ModeAutoPilot, ModeReadOnly, ModeDontAsk, ModeBypassPermissions} {
		for _, tool := range []string{"Read", "Edit", "Bash"} {
			for _, d := range []PermissionDecision{ModeDefault(tool, mode), ModeDefaultForCall(tool, nil, mode), ModeDefaultForCall("Bash", map[string]any{"command": "git status"}, mode), ModeDefaultForCall("Cron", map[string]any{"action": "list"}, mode)} {
				if d.By != "mode" || d.Scope != "" {
					t.Errorf("mode %v, tool %s: unexpected provenance %+v", mode, tool, d)
				}
			}
		}
	}
}
