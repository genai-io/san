package worktree

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed: %s", args, out)
		}
	}
	return dir
}

// Every git call in this package used exec.Command with no context, and both
// tool entry points discarded theirs (`Execute(_ context.Context, ...)`). A
// user pressing Esc could not interrupt the turn: the agent goroutine sat in
// cmd.Output() until git returned on its own, which for `git worktree add`
// behind a post-checkout hook, or `git status` behind an lfs smudge filter,
// can be a long time.
func TestGitCallsHonourACancelledContext(t *testing.T) {
	repo := gitRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled, as it would be after Esc

	if _, _, err := Create(ctx, repo, "cancelled"); err == nil {
		t.Error("Create ignored a cancelled context")
	}
	if err := Remove(ctx, repo, repo+"/.git/agent-worktrees/cancelled"); err == nil {
		t.Error("Remove ignored a cancelled context")
	}
	// The predicates fail closed rather than returning an error, so assert they
	// return promptly instead of blocking on git.
	done := make(chan struct{})
	go func() {
		HasUncommittedChanges(ctx, repo)
		HasUnmergedCommits(ctx, repo)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("the status predicates blocked despite a cancelled context")
	}
}

// A live context still works end to end.
func TestCreateAndRemoveWithALiveContext(t *testing.T) {
	repo := gitRepo(t)
	ctx := context.Background()

	result, cleanup, err := Create(ctx, repo, "live")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer cleanup()

	if HasUncommittedChanges(ctx, result.Path) {
		t.Error("a fresh worktree should be clean")
	}
	if err := Remove(ctx, repo, result.Path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}
