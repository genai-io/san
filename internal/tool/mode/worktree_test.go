package mode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/v2/internal/tool"
)

func runGitToolTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func makeToolTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitToolTest(t, repo, "init")
	runGitToolTest(t, repo, "config", "user.name", "San Tests")
	runGitToolTest(t, repo, "config", "user.email", "tests@example.com")
	filePath := filepath.Join(repo, "README.md")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitToolTest(t, repo, "add", "README.md")
	runGitToolTest(t, repo, "commit", "-m", "init")
	return repo
}

func TestEnterWorktreePrepareInteractionUsesNameParam(t *testing.T) {
	wt := &EnterWorktreeTool{}

	request, err := wt.PrepareInteraction(context.Background(), map[string]any{"name": "feature-slug"}, "/repo")
	if err != nil {
		t.Fatalf("PrepareInteraction() error: %v", err)
	}

	req, ok := request.(*tool.EnterWorktreeRequest)
	if !ok {
		t.Fatalf("expected *tool.EnterWorktreeRequest, got %T", request)
	}
	if req.Slug != "feature-slug" {
		t.Fatalf("expected slug %q, got %q", "feature-slug", req.Slug)
	}
}

func TestEnterWorktreeExecuteWithResponse(t *testing.T) {
	wt := &EnterWorktreeTool{}

	t.Run("declined", func(t *testing.T) {
		result := wt.ExecuteWithResponse(context.Background(), nil, &tool.EnterWorktreeResponse{Approved: false}, "/repo")
		if !result.Success {
			t.Fatal("expected declined response to return a successful noop result")
		}
		if !strings.Contains(result.Output, "User declined entering worktree") {
			t.Fatalf("unexpected declined output: %q", result.Output)
		}
	})

	t.Run("approved", func(t *testing.T) {
		result := wt.ExecuteWithResponse(context.Background(), nil, &tool.EnterWorktreeResponse{
			Approved:     true,
			WorktreePath: "/repo/.git/agent-worktrees/feature-slug",
		}, "/repo")
		if !result.Success {
			t.Fatal("expected approved response to succeed")
		}
		if !strings.Contains(result.Output, "Switched to worktree at /repo/.git/agent-worktrees/feature-slug") {
			t.Fatalf("unexpected approved output: %q", result.Output)
		}
		if result.Metadata.Subtitle != "/repo/.git/agent-worktrees/feature-slug" {
			t.Fatalf("expected subtitle to contain worktree path, got %q", result.Metadata.Subtitle)
		}
		resp, ok := result.HookResponse.(map[string]any)
		if !ok || resp["worktreePath"] != "/repo/.git/agent-worktrees/feature-slug" {
			t.Fatalf("expected hook response to expose worktree path, got %#v", result.HookResponse)
		}
	})
}

func TestExitWorktreePrepareInteraction(t *testing.T) {
	ewt := &ExitWorktreeTool{}

	t.Run("defaults to remove", func(t *testing.T) {
		request, err := ewt.PrepareInteraction(context.Background(), nil, "/repo")
		if err != nil {
			t.Fatalf("PrepareInteraction() error: %v", err)
		}

		req, ok := request.(*tool.ExitWorktreeRequest)
		if !ok {
			t.Fatalf("expected *tool.ExitWorktreeRequest, got %T", request)
		}
		if req.Action != "remove" {
			t.Fatalf("expected default action %q, got %q", "remove", req.Action)
		}
		if req.DiscardChanges {
			t.Fatal("expected discard_changes to default false")
		}
	})

	t.Run("parses action and discard flag", func(t *testing.T) {
		request, err := ewt.PrepareInteraction(context.Background(), map[string]any{
			"action":          "keep",
			"discard_changes": true,
		}, "/repo")
		if err != nil {
			t.Fatalf("PrepareInteraction() error: %v", err)
		}

		req := request.(*tool.ExitWorktreeRequest)

		if req.Action != "keep" || !req.DiscardChanges {
			t.Fatalf("unexpected request: %+v", req)
		}
	})

	t.Run("rejects invalid action", func(t *testing.T) {
		_, err := ewt.PrepareInteraction(context.Background(), map[string]any{"action": "archive"}, "/repo")
		if err == nil {
			t.Fatal("expected invalid action error")
		}
		if !strings.Contains(err.Error(), "action must be 'keep' or 'remove'") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestExitWorktreeExecuteWithResponse(t *testing.T) {
	ewt2 := &ExitWorktreeTool{}

	t.Run("declined", func(t *testing.T) {
		result := ewt2.ExecuteWithResponse(context.Background(), nil, &tool.ExitWorktreeResponse{Approved: false}, "/repo")
		if !result.Success {
			t.Fatal("expected declined exit to return a successful noop result")
		}
		if !strings.Contains(result.Output, "User declined exiting worktree") {
			t.Fatalf("unexpected declined output: %q", result.Output)
		}
	})

	t.Run("approved", func(t *testing.T) {
		result := ewt2.ExecuteWithResponse(context.Background(), nil, &tool.ExitWorktreeResponse{
			Approved:     true,
			RestoredPath: "/repo",
		}, "/repo")
		if !result.Success {
			t.Fatal("expected approved exit to succeed")
		}
		if !strings.Contains(result.Output, "Exited worktree. Restored working directory to /repo.") {
			t.Fatalf("unexpected approved output: %q", result.Output)
		}
		if result.Metadata.Subtitle != "restored: /repo" {
			t.Fatalf("expected restored subtitle, got %q", result.Metadata.Subtitle)
		}
		resp, ok := result.HookResponse.(map[string]any)
		if !ok || resp["restoredPath"] != "/repo" {
			t.Fatalf("expected hook response to expose restored path, got %#v", result.HookResponse)
		}
	})
}

func TestExitWorktreeExecuteManagedWorktree(t *testing.T) {
	repo := makeToolTestRepo(t)
	enter := &EnterWorktreeTool{}
	exit := &ExitWorktreeTool{}

	enterResult := enter.Execute(context.Background(), map[string]any{"name": "feature-slug"}, repo)
	if !enterResult.Success {
		t.Fatalf("enter worktree failed: %s", enterResult.Error)
	}
	resp, ok := enterResult.HookResponse.(map[string]any)
	if !ok {
		t.Fatalf("expected enter hook response, got %#v", enterResult.HookResponse)
	}
	worktreePath, _ := resp["worktreePath"].(string)
	if worktreePath == "" {
		t.Fatalf("expected worktree path in hook response, got %#v", resp)
	}

	exitResult := exit.Execute(context.Background(), map[string]any{"action": "keep"}, worktreePath)
	if !exitResult.Success {
		t.Fatalf("exit worktree failed: %s", exitResult.Error)
	}
	if !strings.Contains(exitResult.Output, "Restored working directory to "+repo) {
		t.Fatalf("unexpected exit output: %q", exitResult.Output)
	}
}
