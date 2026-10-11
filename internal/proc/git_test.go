package proc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseGitStatus(t *testing.T) {
	output := strings.Join([]string{
		"# branch.oid 0123456789abcdef",
		"# branch.head feature/topic",
		"# branch.upstream origin/feature/topic",
		"# branch.ab +2 -3",
		"1 M. N... 100644 100644 100644 abc abc staged",
		"1 .M N... 100644 100644 100644 abc abc modified",
		"1 MM N... 100644 100644 100644 abc abc both",
		"2 R. N... 100644 100644 100644 abc abc R100 renamed",
		"? original filename", // the rename's original path, not an untracked file
		"? new\nfile.txt",
		"u UU N... 100644 100644 100644 100644 abc abc abc conflict",
		"! ignored",
		"",
	}, "\x00")
	got := parseGitStatus(output)
	want := GitStatus{Branch: "feature/topic", Staged: 3, Modified: 2, Untracked: 1, Conflicts: 1, Ahead: 2, Behind: 3}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestParseGitStatusHEADStates(t *testing.T) {
	for _, tt := range []struct{ name, oid, head, want string }{
		{"unborn", "(initial)", "main", "main"},
		{"detached", "0123456789abcdef", "(detached)", "0123456 (detached)"},
		{"short oid", "abc", "(detached)", "abc (detached)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatus("# branch.oid " + tt.oid + "\x00# branch.head " + tt.head + "\x00")
			if got.Branch != tt.want {
				t.Fatalf("branch = %q, want %q", got.Branch, tt.want)
			}
		})
	}
}

func TestGitRemoteLabel(t *testing.T) {
	for _, tt := range []struct{ remote, want string }{
		{"git@github.com:genai-io/san.git", "github.com/genai-io/san"},
		{"github.com:genai-io/san.git", "github.com/genai-io/san"},
		{"ssh://git@github.com:2222/genai-io/san.git", "github.com/genai-io/san"},
		{"https://user:private-token@github.com/genai-io/san.git?access_token=private-token#secret", "github.com/genai-io/san"},
		{"https://git.example.com/team/project.git", "git.example.com/team/project"},
		{"file:///private/work/repo.git", "repo"},
		{"file://localhost/private/work/repo.git", "repo"},
		{"ext::sh private-command", ""},
		{"/private/work/repo.git", "repo"},
		{`C:\private\work\repo.git`, "repo"},
		{"", ""},
		{"https://bad%host/repo", ""},
	} {
		t.Run(tt.remote, func(t *testing.T) {
			if got := gitRemoteLabel(tt.remote); got != tt.want {
				t.Fatalf("label = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGitRepositoryLabelPrefersTrackingRemote(t *testing.T) {
	config := "remote.origin.url\ngit@github.com:fork/repo.git\x00" +
		"remote.upstream.url\nhttps://github.com/main/repo.git\x00" +
		"branch.feature/topic.remote\nupstream\x00"
	if got := gitRepositoryLabel(config, "feature/topic"); got != "github.com/main/repo" {
		t.Fatalf("tracking remote label = %q", got)
	}
	if got := gitRepositoryLabel(config, "other"); got != "github.com/fork/repo" {
		t.Fatalf("origin fallback = %q", got)
	}
	if got := gitRepositoryLabel("remote.other.url\ngit@github.com:other/repo.git\x00", "main"); got != "github.com/other/repo" {
		t.Fatalf("non-origin fallback = %q", got)
	}
}

func TestReadGitStatus(t *testing.T) {
	repo := initStatusRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := ReadGitStatus(ctx, repo)
	if err != nil || got.Branch != "main" || got.Repository != filepath.Base(repo) {
		t.Fatalf("unborn repository = %#v, err = %v", got, err)
	}
	writeStatusFile(t, repo, "tracked.txt", "initial")
	statusGit(t, repo, "add", "tracked.txt")
	statusGit(t, repo, "commit", "-m", "initial")
	writeStatusFile(t, repo, "tracked.txt", "changed")
	writeStatusFile(t, repo, "staged.txt", "staged")
	statusGit(t, repo, "add", "staged.txt")
	writeStatusFile(t, repo, "untracked.txt", "new")
	statusGit(t, repo, "remote", "add", "origin", "https://user:private-token@github.com/owner/repo.git?token=private-token")
	subdir := filepath.Join(repo, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Repository discovery must follow cwd, not an inherited Git environment.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-a-repo"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	got, err = ReadGitStatus(ctx, subdir)
	if err != nil {
		t.Fatal(err)
	}
	want := GitStatus{Branch: "main", Repository: "github.com/owner/repo", Staged: 1, Modified: 1, Untracked: 1}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestReadGitStatusDetachedWorktree(t *testing.T) {
	repo := initStatusRepo(t)
	writeStatusFile(t, repo, "tracked.txt", "initial")
	statusGit(t, repo, "add", "tracked.txt")
	statusGit(t, repo, "commit", "-m", "initial")
	worktree := filepath.Join(t.TempDir(), "worktree")
	statusGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := ReadGitStatus(ctx, worktree)
	if err != nil || !strings.HasSuffix(got.Branch, " (detached)") || got.Repository != "worktree" {
		t.Fatalf("detached worktree = %#v, err = %v", got, err)
	}
}

func TestReadGitStatusUnavailable(t *testing.T) {
	t.Run("outside repository", func(t *testing.T) {
		got, err := ReadGitStatus(context.Background(), t.TempDir())
		if err == nil || got != (GitStatus{}) {
			t.Fatalf("non-repository = %#v, err = %v", got, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := ReadGitStatus(ctx, t.TempDir())
		if err == nil || got != (GitStatus{}) {
			t.Fatalf("cancelled read = %#v, err = %v", got, err)
		}
	})
	t.Run("missing git", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got, err := ReadGitStatus(context.Background(), t.TempDir())
		if err == nil || got != (GitStatus{}) {
			t.Fatalf("missing git = %#v, err = %v", got, err)
		}
	})
}

func TestReadGitStatusTimeoutStopsCleanFilter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("clean filter fixture uses a POSIX shell")
	}
	repo := initStatusRepo(t)
	writeStatusFile(t, repo, "tracked", "initial")
	statusGit(t, repo, "add", "tracked")
	statusGit(t, repo, "commit", "-m", "initial")
	writeStatusFile(t, repo, ".gitattributes", "tracked filter=review\n")
	statusGit(t, repo, "config", "filter.review.clean",
		"printf started > filter-started; sleep 3; printf survived > filter-survived; cat")
	writeStatusFile(t, repo, "tracked", "changed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	got, err := ReadGitStatus(ctx, repo)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("read exceeded its deadline: %s", elapsed)
	}
	if err == nil || got != (GitStatus{}) || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("timed-out read = %#v, err = %v, context = %v", got, err, ctx.Err())
	}
	if _, err := os.Stat(filepath.Join(repo, "filter-started")); err != nil {
		t.Fatalf("clean filter never started: %v", err)
	}
	// Wait past the filter's write to catch children left running after cancellation.
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(filepath.Join(repo, "filter-survived")); !os.IsNotExist(err) {
		t.Fatalf("clean filter survived cancellation: %v", err)
	}
}

func initStatusRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	statusGit(t, repo, "init", "-b", "main")
	statusGit(t, repo, "config", "user.name", "Test")
	statusGit(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func statusGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	// Ignore the developer's signing, hooks and default template settings.
	args = append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "-c", "init.templateDir="}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeStatusFile(t *testing.T, cwd, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
