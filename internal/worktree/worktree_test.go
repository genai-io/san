package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func makeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "San Tests")
	runGit(t, repo, "config", "user.email", "tests@example.com")

	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "init")

	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, repo, "init", "--bare", origin)
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	return repo
}

func TestCreateAndRemoveWorktree(t *testing.T) {
	repo := makeRepo(t)

	result, _, err := Create(repo, "feature-slug")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	wantPath := filepath.Join(repo, ".git", worktreeDir, "feature-slug")
	if result.Path != wantPath {
		t.Fatalf("expected worktree path %q, got %q", wantPath, result.Path)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("expected worktree to exist: %v", err)
	}
	if result.Branch != "" {
		t.Fatalf("expected detached worktree branch to be empty, got %q", result.Branch)
	}

	if err := Remove(repo, result.Path); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree path to be removed, stat err=%v", err)
	}
}

func TestCreateRequiresGitRepo(t *testing.T) {
	nonRepo := t.TempDir()

	_, _, err := Create(nonRepo, "slug")
	if err == nil {
		t.Fatal("expected Create() to fail outside a git repo")
	}
	if !strings.Contains(err.Error(), "resolve git common dir") {
		t.Fatalf("expected git worktree failure, got %v", err)
	}
}

func TestCreateFromLinkedWorktree(t *testing.T) {
	repo := makeRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, repo, "worktree", "add", "--detach", linked)

	result, cleanup, err := Create(linked, "from-linked")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	defer cleanup()

	commonDir := runGit(t, linked, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(linked, commonDir)
	}
	wantPath := filepath.Join(filepath.Clean(commonDir), worktreeDir, "from-linked")
	if result.Path != wantPath {
		t.Fatalf("worktree path = %q, want %q", result.Path, wantPath)
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	repo := makeRepo(t)
	result, cleanup, err := Create(repo, "dirty-check")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	defer cleanup()

	if HasUncommittedChanges(result.Path) {
		t.Fatal("expected fresh worktree to be clean")
	}

	filePath := filepath.Join(result.Path, "README.md")
	if err := os.WriteFile(filePath, []byte("updated\n"), 0o644); err != nil {
		t.Fatalf("write README in worktree: %v", err)
	}

	if !HasUncommittedChanges(result.Path) {
		t.Fatal("expected modified worktree to be dirty")
	}
}

func TestHasUnmergedCommits(t *testing.T) {
	repo := makeRepo(t)
	result, cleanup, err := Create(repo, "commit-check")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	defer cleanup()

	if HasUnmergedCommits(result.Path) {
		t.Fatal("expected fresh worktree to have no unmerged commits")
	}

	runGit(t, result.Path, "config", "user.name", "San Tests")
	runGit(t, result.Path, "config", "user.email", "tests@example.com")
	filePath := filepath.Join(result.Path, "feature.txt")
	if err := os.WriteFile(filePath, []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGit(t, result.Path, "add", "feature.txt")
	runGit(t, result.Path, "commit", "-m", "feature")

	if !HasUnmergedCommits(result.Path) {
		t.Fatal("expected local worktree commit to be reported as unmerged")
	}
}

func TestOriginalPath(t *testing.T) {
	repo := "/tmp/repo"
	worktreePath := filepath.Join(repo, ".git", worktreeDir, "feature-slug")

	original, ok := OriginalPath(worktreePath)
	if !ok {
		t.Fatal("expected managed worktree path to resolve")
	}
	if original != repo {
		t.Fatalf("expected original path %q, got %q", repo, original)
	}

	if _, ok := OriginalPath(filepath.Join(repo, "nested")); ok {
		t.Fatal("expected non-managed path to be rejected")
	}
}
