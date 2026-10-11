package conv

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderGitBar(t *testing.T) {
	got := RenderGitBar(GitBarParams{
		Width: 120, Branch: "feature/topic", Repository: "github.com/genai-io/san",
		Staged: 2, Modified: 3, Untracked: 1, Conflicts: 1, Ahead: 4, Behind: 5,
	})
	if got != ansi.Strip(got) || strings.Contains(got, "git ") {
		t.Fatalf("bar must use plain text without a redundant prefix: %q", got)
	}
	for _, want := range []string{"feature/topic", "github.com/genai-io/san", "+2", "~3", "?1", "!1", "↑4", "↓5"} {
		if !strings.Contains(got, want) {
			t.Errorf("bar missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "clean") {
		t.Fatalf("dirty worktree reported as clean: %q", got)
	}
}

func TestRenderGitBarUnchangedAndUnavailable(t *testing.T) {
	if got := ansi.Strip(RenderGitBar(GitBarParams{Width: 80, Branch: "main"})); got != "  main" {
		t.Fatalf("unchanged worktree should show only its branch: %q", got)
	}
	if got := ansi.Strip(RenderGitBar(GitBarParams{Width: 80, Branch: "main", Repository: "github.com/owner/repo"})); got != "  main · github.com/owner/repo" {
		t.Fatalf("unchanged worktree should omit redundant status and separators: %q", got)
	}
	if got := ansi.Strip(RenderGitBar(GitBarParams{Width: 80, Branch: "main", Ahead: 1})); got != "  main · ↑1" {
		t.Fatalf("upstream difference without changes = %q", got)
	}
	if got := RenderGitBar(GitBarParams{Width: 80}); got != "" {
		t.Fatalf("unavailable repository should have no row, got %q", got)
	}
}

func TestRenderGitBarFitsWidth(t *testing.T) {
	for _, width := range []int{0, 5, 8, 9, 12, 20, 40, 80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			got := RenderGitBar(GitBarParams{
				Width: width, Branch: strings.Repeat("中文分支", 20), Repository: strings.Repeat("github.com/owner/repo", 10),
				Staged: 12, Modified: 34, Untracked: 56, Conflicts: 78, Ahead: 90, Behind: 12,
			})
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("bar must stay on one row: %q", got)
			}
			if gotWidth := lipgloss.Width(got); gotWidth > max(0, width-1) {
				t.Fatalf("bar width = %d, exceeds %d: %q", gotWidth, width-1, got)
			}
		})
	}
}

func TestRenderGitBarKeepsStatusBeforeRepository(t *testing.T) {
	got := ansi.Strip(RenderGitBar(GitBarParams{
		Width: 35, Branch: strings.Repeat("long-branch-", 10), Repository: "github.com/owner/repo", Modified: 3,
	}))
	if !strings.Contains(got, "~3") {
		t.Fatalf("long branch crowded out status: %q", got)
	}
}

func TestRenderGitBarRemovesControlCharacters(t *testing.T) {
	got := RenderGitBar(GitBarParams{Width: 100, Branch: "main\n\r\t", Repository: "github.com/owner/\x1brepo\n"})
	plain := ansi.Strip(got)
	if strings.ContainsAny(plain, "\n\r\t\x1b") {
		t.Fatalf("control characters leaked into footer: %q", plain)
	}
}
