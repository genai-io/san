package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeUsesCheckedOutMCPConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	config := func(root, command string) {
		t.Helper()
		loader := NewConfigLoader(root)
		if err := loader.SaveServer("project", ServerConfig{Command: command}, ScopeProject); err != nil {
			t.Fatal(err)
		}
	}
	config(repo, "checked-out-command")
	git("add", ".san/mcp.json")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "MCP configuration")
	worktree := filepath.Join(t.TempDir(), "worktree")
	git("worktree", "add", "-b", "feature", worktree)

	load := func(root string) map[string]ServerConfig {
		t.Helper()
		servers, err := NewConfigLoader(root).LoadAll()
		if err != nil {
			t.Fatal(err)
		}
		return servers
	}
	if _, exists := load(worktree)["project"]; exists {
		t.Fatal("checked-out MCP bypassed workspace trust")
	}
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	if got := load(worktree)["project"].Command; got != "checked-out-command" {
		t.Fatalf("worktree config = %q", got)
	}

	// Git supplies a checked-out file, not a live link to the main checkout.
	config(repo, "main-only-change")
	if got := load(worktree)["project"].Command; got != "checked-out-command" {
		t.Fatalf("main checkout unexpectedly overrode worktree: %q", got)
	}
	config(worktree, "worktree-change")
	if got := load(repo)["project"].Command; got != "main-only-change" {
		t.Fatalf("worktree write changed main configuration: %q", got)
	}
	manager, err := NewSession(worktree, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, ok := manager.Config("project")
	if !ok || server.Command != "worktree-change" || server.CWD != worktree {
		t.Fatalf("session MCP configuration = %+v, found=%v", server, ok)
	}
	if err := os.Remove(NewConfigLoader(worktree).FilePath(ScopeProject)); err != nil {
		t.Fatal(err)
	}
	if _, exists := load(worktree)["project"]; exists {
		t.Fatal("missing project file silently fell back to main checkout")
	}
}
