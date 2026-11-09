package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenStoreSharedWorktrees(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	repo, worktree := filepath.Join(root, "repo"), filepath.Join(root, "worktree")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	git("init", repo)
	git("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	git("-C", repo, "worktree", "add", "-b", "fix", worktree)

	// First opening a linked worktree still registers the original project.
	child, err := OpenStore(ctx, worktree)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	main, err := OpenStore(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	if main.projectID != child.projectID || main.projectDir != child.projectDir {
		t.Fatal("linked worktrees did not share project storage")
	}
	opened, err := child.Create(CreateOptions{ID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, history, err := main.Open("child")
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	wantWorkspace, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if history.Metadata.Workspace != wantWorkspace || history.Metadata.Cwd != wantWorkspace || history.Metadata.ProjectID != main.projectID {
		t.Fatalf("resume replaced original identity: %+v", history.Metadata)
	}
	other, err := main.Create(CreateOptions{ID: "explicit", Workspace: worktree})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := main.Create(CreateOptions{ID: "foreign", Workspace: t.TempDir()}); err == nil {
		t.Fatal("accepted workspace outside the project")
	}
	list, err := main.ListContext(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("project list = %+v, %v", list, err)
	}
}

func TestProjectMovedGitMetadataIsANewProject(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	projectTestGit(t, repo, "init", "-q")
	original := projectTestResolve(t, home, repo)
	metadata := filepath.Join(t.TempDir(), "metadata")
	projectTestGit(t, repo, "init", "-q", "--separate-git-dir", metadata)
	projectTestGit(t, repo, "config", "core.worktree", repo)
	moved := projectTestResolve(t, home, repo)
	if moved.ID == original.ID || moved.Key == original.Key {
		t.Fatal("moved Git metadata kept the old identity")
	}
}

func TestOpenStoreExactDirectoryAndCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenStore(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.projectID == b.projectID {
		t.Fatal("non-Git child directory inherited parent project")
	}
	c, err := OpenStore(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.projectID != a.projectID {
		t.Fatal("same startup directory did not reuse project")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := OpenStore(cancelled, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open = %v", err)
	}
}
