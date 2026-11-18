package fs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestGitBareRepositoryGuardAppendsToUserConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	got := gitBareRepositoryGuard()
	want := []string{"GIT_CONFIG_KEY_2=safe.bareRepository", "GIT_CONFIG_VALUE_2=explicit", "GIT_CONFIG_COUNT=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("guard = %v, want %v", got, want)
	}
	t.Setenv("GIT_CONFIG_COUNT", "bogus")
	if got := gitBareRepositoryGuard(); got != nil {
		t.Fatalf("malformed count must be left alone, got %v", got)
	}
}

// A repository can check in the files of a bare repository whose config sets
// diff.external. A read the permission gate runs unprompted must not run it.
func TestBashDoesNotRunCheckedInBareRepositoryConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	os.Unsetenv("GIT_CONFIG_COUNT")
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "src")
	run("-C", "src", "commit", "-q", "--allow-empty", "-m", "a")
	if err := os.WriteFile(filepath.Join(dir, "src", "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", "src", "add", "f")
	run("-C", "src", "commit", "-q", "-m", "b")
	run("clone", "-q", "--bare", "src", "vendor")
	marker := filepath.Join(dir, "ran")
	run("-C", "vendor", "config", "diff.external", "touch "+marker+" #")

	bashTool(t).ExecuteApproved(context.Background(), map[string]any{"command": "cd vendor && git diff HEAD~1 HEAD"}, dir)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("diff.external from a checked-in bare repository ran")
	}
}
