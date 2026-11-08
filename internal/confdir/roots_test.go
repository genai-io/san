package confdir

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoRootRealWorktrees(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "separate-git-dir"}[separate], func(t *testing.T) {
			base := t.TempDir()
			main, wt := filepath.Join(base, "main"), filepath.Join(base, "linked")
			git := func(dir string, args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			args := []string{"init", "-q"}
			if separate {
				args = append(args, "--separate-git-dir", filepath.Join(base, "metadata"))
			}
			git(base, append(args, main)...)
			git(main, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
			git(main, "worktree", "add", "-qb", "feature", wt)
			sub := filepath.Join(wt, "sub")
			if err := os.Mkdir(sub, 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(base, "alias")
			if err := os.Symlink(wt, alias); err != nil {
				t.Fatal(err)
			}
			want, _ := filepath.EvalSymlinks(wt)
			mainWant := want
			if !separate {
				mainWant, _ = filepath.EvalSymlinks(main)
			}
			if got := MainRepoRoot(wt); got != mainWant {
				t.Fatalf("trust root=%s want %s", got, mainWant)
			}
			for cwd, want := range map[string]string{wt: wt, sub: wt, alias: alias} {
				if got := RepoRoot(cwd); got != want {
					t.Fatalf("RepoRoot(%s)=%s want lexical %s", cwd, got, want)
				}
			}
			t.Chdir(base)
			if got := RepoRoot("linked/sub"); got != "linked" {
				t.Fatalf("relative root=%s", got)
			}
		})
	}
}

func TestRepoRootNonGitExact(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(filepath.Join(parent, ".san"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	want := child
	if got := RepoRoot(child); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRepoRootMalformedMetadataStaysAtCheckout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing"), 0600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	want := root
	if got := RepoRoot(sub); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
