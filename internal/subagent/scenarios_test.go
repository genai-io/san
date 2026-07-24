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
			name: "explore + allow git diff* — compound with mutating tail remains blocked",
			mode: PermissionExplore, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && npm run build"},
			want: false, wantMatch: "denied in Explore",
		},
		{
			name: "explore + allow git diff* — read-only compound bypasses whitelist",
			mode: PermissionExplore, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && git status"},
			want: true,
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

		// ── 3. bypass skips confirmation tiers; the circuit breaker holds ──
		{
			name: "rm -rf on a subpath in compound bash — bypass allows",
			mode: PermissionBypass, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git diff && rm -rf /tmp/dummy"},
			want: true,
		},
		{
			name: "git push --force — bypass allows without confirmation",
			mode: PermissionBypass, allow: ToolList{{Name: "Bash"}},
			tool: "Bash", input: map[string]any{"command": "git push --force origin main"},
			want: true,
		},
		{
			name: "rm -rf ~ — circuit breaker blocks even in bypass",
			mode: PermissionBypass, allow: ToolList{{Name: "Bash"}},
			tool: "Bash", input: map[string]any{"command": "rm -rf ~"},
			want: false, wantMatch: "blocked",
		},
		{
			name: "git push --force — non-bypass hits the confirmation tier",
			mode: PermissionDefault, allow: ToolList{{Name: "Bash"}},
			tool: "Bash", input: map[string]any{"command": "git push --force origin main"},
			want: false, wantMatch: "blocked",
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
			name: "bypassPermissions — Bash unconstrained",
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
			name: "default mode + allow Bash(git diff*) — mutating command hits whitelist constraint",
			mode: PermissionDefault, allow: allowGitDiff,
			tool: "Bash", input: map[string]any{"command": "git commit -m msg"},
			want: false, wantMatch: "outside the allow_tools constraint",
		},

		// ── read-only bash — permitted like the dedicated read-only tools ──
		{
			name: "explore mode — read-only search allowed without allow_tools",
			mode: PermissionExplore,
			tool: "Bash", input: map[string]any{"command": "rg -n TODO internal | head -50"},
			want: true,
		},
		{
			name: "default mode — read-only git allowed without allow_tools",
			mode: PermissionDefault,
			tool: "Bash", input: map[string]any{"command": "git status"},
			want: true,
		},

		// ── parent-only and Skill ──
		{
			name: "tracker write denied for worker even in bypass mode",
			mode: PermissionBypass,
			tool: "TaskCreate", input: map[string]any{"subject": "x", "description": "y"},
			want: false, wantMatch: "reserved for the main conversation",
		},
		{
			name: "cron denied for worker in every mode",
			mode: PermissionExplore,
			tool: "Cron", input: map[string]any{"action": "list"},
			want: false, wantMatch: "reserved for the main conversation",
		},
		{
			name: "explore mode — Skill allowed without allow_tools",
			mode: PermissionExplore,
			tool: "Skill", input: map[string]any{"skill": "commit"},
			want: true,
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
