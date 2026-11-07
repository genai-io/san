package confdir

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConfigRootRealWorktrees(t *testing.T) {
	for _, separate := range []bool{false, true} {
		name := "normal"
		if separate {
			name = "separate-git-dir"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			main := filepath.Join(base, "main")
			wt := filepath.Join(base, "linked")
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
			args = append(args, main)
			git(base, args...)
			git(main, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
			git(main, "worktree", "add", "-qb", "feature", wt)
			if separate {
				if _, err := ConfigRoot(wt); err == nil {
					t.Fatal("accepted ambiguous separate-git-dir without core.worktree")
				}
				git(main, "config", "core.worktree", main)
			}
			sub := filepath.Join(wt, "sub")
			if err := os.Mkdir(sub, 0700); err != nil {
				t.Fatal(err)
			}
			for _, cwd := range []string{wt, sub, main} {
				got, err := ConfigRoot(cwd)
				if err != nil || got != canonical(main) {
					t.Fatalf("ConfigRoot(%s)=%s,%v", cwd, got, err)
				}
			}
			// A surviving directory at a stale original location is not enough.
			if err := os.Rename(main, filepath.Join(base, "moved")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(main, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := ConfigRoot(wt); err == nil {
				t.Fatal("accepted missing main checkout metadata")
			}
		})
	}
}

func TestConfigRootSubmodule(t *testing.T) {
	base := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for _, name := range []string{"parent", "source"} {
		git(base, "init", "-q", name)
		git(filepath.Join(base, name), "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	}
	parent := filepath.Join(base, "parent")
	git(parent, "-c", "protocol.file.allow=always", "submodule", "add", filepath.Join(base, "source"), "module")
	module := filepath.Join(parent, "module")
	got, err := ConfigRoot(module)
	if err != nil || got != canonical(module) {
		t.Fatalf("submodule root=%q, %v", got, err)
	}
	wt := filepath.Join(base, "module-worktree")
	git(module, "worktree", "add", "-qb", "feature", wt)
	got, err = ConfigRoot(wt)
	if err != nil || got != canonical(module) {
		t.Fatalf("submodule worktree root=%q, %v", got, err)
	}
}

func TestConfigRootNonGitExact(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(filepath.Join(parent, ".san"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ConfigRoot(child)
	if err != nil || got != canonical(child) {
		t.Fatalf("got %q,%v", got, err)
	}
}
