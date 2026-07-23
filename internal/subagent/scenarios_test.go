package subagent

import (
	"context"
	"strings"
	"testing"
)

// TestPermissionScenarios walks the fixed subagent permission modes against the
// actual gate. Subagents have no per-definition allow/deny rules; mode is the
// only worker-specific permission input.
func TestPermissionScenarios(t *testing.T) {
	type scenario struct {
		name      string
		mode      PermissionMode
		tool      string
		input     map[string]any
		want      bool
		wantMatch string
	}

	cases := []scenario{
		{
			name: "bypass allows ordinary Bash",
			mode: PermissionBypass,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			want: true,
		},
		{
			name: "bypass allows work-discarding git",
			mode: PermissionBypass,
			tool: "Bash", input: map[string]any{"command": "git push --force origin main"},
			want: true,
		},
		{
			name: "circuit breaker holds in bypass",
			mode: PermissionBypass,
			tool: "Bash", input: map[string]any{"command": "rm -rf ~"},
			wantMatch: "blocked",
		},
		{
			name: "default allows Read",
			mode: PermissionDefault,
			tool: "Read", input: map[string]any{"file_path": "README.md"},
			want: true,
		},
		{
			name: "default allows read-only Bash",
			mode: PermissionDefault,
			tool: "Bash", input: map[string]any{"command": "git status"},
			want: true,
		},
		{
			name: "default denies a call that needs approval",
			mode: PermissionDefault,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			wantMatch: "would require approval",
		},
		{
			name: "explore allows Read",
			mode: PermissionExplore,
			tool: "Read", input: map[string]any{"file_path": "README.md"},
			want: true,
		},
		{
			name: "explore denies Write",
			mode: PermissionExplore,
			tool: "Write", input: map[string]any{"file_path": "/tmp/foo", "content": "x"},
			wantMatch: "denied in Explore",
		},
		{
			name: "edit allows Write",
			mode: PermissionAcceptEdits,
			tool: "Write", input: map[string]any{"file_path": "/tmp/foo", "content": "x"},
			want: true,
		},
		{
			name: "edit still denies Bash needing approval",
			mode: PermissionAcceptEdits,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			wantMatch: "would require approval",
		},
		{
			name: "parent-only tool stays blocked in bypass",
			mode: PermissionBypass,
			tool: "Agent", input: map[string]any{},
			wantMatch: "reserved for the main conversation",
		},
		{
			name: "Skill is available in explore",
			mode: PermissionExplore,
			tool: "Skill", input: map[string]any{"skill": "commit"},
			want: true,
		},
	}

	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			gate := subagentPermissionFunc(sc.mode)
			got, reason := gate(context.Background(), sc.tool, sc.input)
			if got != sc.want {
				t.Fatalf("got allow=%v want %v (reason=%q)", got, sc.want, reason)
			}
			if !got && sc.wantMatch != "" && !strings.Contains(reason, sc.wantMatch) {
				t.Fatalf("reason %q does not contain %q", reason, sc.wantMatch)
			}
		})
	}
}
