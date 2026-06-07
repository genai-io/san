package subagent

import (
	"context"
	"strings"
	"testing"
)

// TestPermissionScenarios is a single table that walks every documented
// scenario in docs/concepts/permission-model.md against the actual subagent gate.
// Run with `go test ./internal/subagent/ -run TestPermissionScenarios -v`
// to see a human-readable truth table.
func TestPermissionScenarios(t *testing.T) {
	type scenario struct {
		name      string
		mode      PermissionMode
		allow     ToolList
		deny      ToolList
		tool      string
		input     map[string]any
		want      bool
		wantMatch string // substring expected in the deny reason (only when want=false)
	}

	allowGitDiff := ToolList{
		{Name: "Read"}, {Name: "Glob"}, {Name: "Grep"},
		{Name: "Bash", Pattern: "git diff*"},
		{Name: "Bash", Pattern: "git log*"},
	}
	denyStash := ToolList{{Name: "Bash", Pattern: "git stash*"}}

	cases := []scenario{
		// ── 1. allow_tools per-subcommand match (the marquee semantic) ──
		{
			name: "explore + allow git diff* — git diff allowed",
			mode: PermissionExplore, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff --stat"},
			want: true,
		},
		{
			name: "explore + allow git diff* — compound with git status fails per-subcommand check",
			mode: PermissionExplore, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && git status"},
			want: false, wantMatch: "outside the allow_tools constraint",
		},
		{
			name: "explore + allow git diff* — both subcommands match",
			mode: PermissionExplore, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && git log --oneline"},
			want: true,
		},

		// ── 2. deny_tools wins over allow + mode ──
		{
			name: "deny git stash* always blocks even with allow + bypass mode",
			mode: PermissionBypass, allow: allowGitDiff, deny: denyStash,
			tool: "Bash", input: map[string]any{"command": "git stash list"},
			want: false, wantMatch: "blocked by deny_tools",
		},

		// ── 3. bypass-immune (destructive) wins over allow ──
		{
			name: "rm -rf in compound bash — bypass-immune blocks",
			mode: PermissionBypass, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && rm -rf /tmp/dummy"},
			want: false, wantMatch: "destructive command",
		},
		{
			name: "git push --force — bypass-immune blocks even with allow Bash",
			mode: PermissionBypass, allow: ToolList{{Name: "Bash"}},
			tool: "Bash", input: map[string]any{"command": "git push --force origin main"},
			want: false, wantMatch: "destructive command",
		},

		// ── 4. mode default behavior ──
		{
			name: "default mode — Read auto-allowed (safe tool)",
			mode: PermissionDefault,
			tool: "Read", input: map[string]any{"file_path": "README.md"},
			want: true,
		},
		{
			name: "default mode — Bash with no allow_tools collapses Ask → Deny in subagent",
			mode: PermissionDefault,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			want: false, wantMatch: "would require approval",
		},
		{
			name: "explore mode — Read still allowed (safe tool)",
			mode: PermissionExplore,
			tool: "Read", input: map[string]any{"file_path": "README.md"},
			want: true,
		},
		{
			name: "explore mode — Write rejected without prompt",
			mode: PermissionExplore,
			tool: "Write", input: map[string]any{"file_path": "/tmp/foo", "content": "x"},
			want: false, wantMatch: "denied in Explore",
		},
		{
			name: "acceptEdits mode — Write auto-allowed",
			mode: PermissionAcceptEdits,
			tool: "Write", input: map[string]any{"file_path": "/tmp/foo", "content": "x"},
			want: true,
		},
		{
			name: "acceptEdits mode — Bash without allow_tools still denied",
			mode: PermissionAcceptEdits,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			want: false, wantMatch: "would require approval",
		},
		{
			name: "bypassPermissions — Bash unconstrained (non-destructive)",
			mode: PermissionBypass,
			tool: "Bash", input: map[string]any{"command": "echo hi"},
			want: true,
		},

		// ── 5. allow_tools whitelist (mode-default fallthrough blocked) ──
		{
			name: "default mode + allow Bash(git diff*) — git diff allowed",
			mode: PermissionDefault, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff --stat"},
			want: true,
		},
		{
			name: "default mode + allow Bash(git diff*) — git status hits whitelist constraint",
			mode: PermissionDefault, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git status"},
			want: false, wantMatch: "outside the allow_tools constraint",
		},
	}

	t.Logf("┌─ Permission gate scenarios (subagent pipeline) ─")
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			gate := subagentPermissionFunc(sc.mode, sc.allow, sc.deny)
			got, reason := gate(context.Background(), sc.tool, sc.input)

			tag := "✓"
			if got != sc.want {
				tag = "✗"
			}
			outcome := "ALLOW"
			if !got {
				outcome = "DENY: " + reason
			}
			t.Logf("│ %s [mode=%-17s] %s(%v) → %s",
				tag, sc.mode, sc.tool, sc.input, outcome)

			if got != sc.want {
				t.Fatalf("got allow=%v want %v (reason=%q)", got, sc.want, reason)
			}
			if !got && sc.wantMatch != "" && !strings.Contains(reason, sc.wantMatch) {
				t.Fatalf("reason %q does not contain %q", reason, sc.wantMatch)
			}
		})
	}
	t.Logf("└─")
}
