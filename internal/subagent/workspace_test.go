package subagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, strings.TrimSpace(string(out)))
	}
}

func makeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "San Tests")
	runGit(t, repo, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "init")
	return repo
}

func TestPrepareWorkspaceRemovesCleanWorktree(t *testing.T) {
	repo := makeRepo(t)
	executor := &Executor{cwd: repo}

	agentCwd, finish, err := executor.prepareWorkspace(tool.AgentExecRequest{Isolation: "worktree"}, nil)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	if agentCwd == repo {
		t.Fatal("worktree isolation should not run in the base cwd")
	}

	if kept := finish(); kept != "" {
		t.Fatalf("clean worktree should be removed, got kept path %q", kept)
	}
	if _, err := os.Stat(agentCwd); !os.IsNotExist(err) {
		t.Fatalf("clean worktree still exists at %s", agentCwd)
	}
}

func TestPrepareWorkspacePreservesDirtyWorktree(t *testing.T) {
	repo := makeRepo(t)
	executor := &Executor{cwd: repo}

	agentCwd, finish, err := executor.prepareWorkspace(tool.AgentExecRequest{Isolation: "worktree"}, nil)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}

	// Simulate the agent editing a file in its worktree.
	if err := os.WriteFile(filepath.Join(agentCwd, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kept := finish()
	if kept != agentCwd {
		t.Fatalf("dirty worktree should be preserved, got %q", kept)
	}
	if _, err := os.Stat(agentCwd); err != nil {
		t.Fatalf("preserved worktree missing: %v", err)
	}
}

func TestPrepareWorkspaceUsesConfigIsolationDefault(t *testing.T) {
	repo := makeRepo(t)
	executor := &Executor{cwd: repo}

	agentCwd, finish, err := executor.prepareWorkspace(
		tool.AgentExecRequest{},
		&AgentConfig{Isolation: "worktree"},
	)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	defer finish()

	if agentCwd == repo {
		t.Fatal("config-level isolation: worktree should apply when the request has none")
	}
}
