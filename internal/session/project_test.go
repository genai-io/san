package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/confdir"
)

func projectTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
func projectTestResolve(t *testing.T, home, cwd string) projectIdentity {
	t.Helper()
	p, err := resolveProject(context.Background(), home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestProjectNonGitExactCanonicalDirectory(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	p := projectTestResolve(t, home, root)
	if q := projectTestResolve(t, home, alias); q.ID != p.ID {
		t.Fatal("symlink changed identity")
	}
	if q := projectTestResolve(t, home, child); q.ID == p.ID {
		t.Fatal("nonGit child inherited project")
	}
	if p.InitialCwd != p.Workspace {
		t.Fatalf("bad identity: %+v", p)
	}
	if _, err := validateProjectWorkspace(context.Background(), p, child); err == nil {
		t.Fatal("accepted foreign directory")
	}
}
func TestProjectGitWorktreesAndSeparateGitDir(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "separate"}[separate], func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			args := []string{"init"}
			if separate {
				args = append(args, "--separate-git-dir="+filepath.Join(t.TempDir(), "metadata"))
			}
			projectTestGit(t, root, args...)
			projectTestGit(t, root, "commit", "--allow-empty", "-m", "initial")
			linked := filepath.Join(t.TempDir(), "linked")
			projectTestGit(t, root, "worktree", "add", "-b", "linked", linked)
			sub := filepath.Join(linked, "sub")
			if err := os.Mkdir(sub, 0700); err != nil {
				t.Fatal(err)
			}
			// Register linked worktree first: identity does not select settings.
			p := projectTestResolve(t, home, sub)
			q := projectTestResolve(t, home, root)
			canonicalRoot, _ := canonicalProjectPath(root)
			canonicalLinked, _ := canonicalProjectPath(linked)
			if p.ID != q.ID || p.Workspace != canonicalLinked || q.Workspace != canonicalRoot {
				t.Fatalf("p=%+v q=%+v", p, q)
			}
			if got, err := validateProjectWorkspace(context.Background(), p, root); err != nil || got != canonicalRoot {
				t.Fatalf("workspace: %q %v", got, err)
			}
			clone := filepath.Join(t.TempDir(), "clone")
			projectTestGit(t, root, "clone", root, clone)
			if r := projectTestResolve(t, home, clone); r.ID == p.ID {
				t.Fatal("clone shared identity")
			}
			module := filepath.Join(root, "module")
			projectTestGit(t, root, "-c", "protocol.file.allow=always", "submodule", "add", clone, "module")
			r := projectTestResolve(t, home, module)
			if r.ID == p.ID {
				t.Fatal("submodule shared identity")
			}
			moduleLinked := filepath.Join(t.TempDir(), "module-linked")
			projectTestGit(t, module, "worktree", "add", "-b", "module-linked", moduleLinked)
			if s := projectTestResolve(t, home, moduleLinked); s.ID != r.ID {
				t.Fatal("submodule worktree lost identity")
			}
			canonicalModuleLinked, _ := canonicalProjectPath(moduleLinked)
			if got := confdir.MainRepoRoot(moduleLinked); got != canonicalModuleLinked {
				t.Fatalf("submodule inferred foreign trust root: %s", got)
			}
		})
	}
}
func TestProjectConcurrentRegistration(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			p, err := resolveProject(context.Background(), home, root)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- p.ID
		})
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate identity")
		}
	}
}
func TestProjectProcessRegistration(t *testing.T) {
	if os.Getenv("SAN_PROJECT_TEST_CHILD") == "1" {
		p, err := resolveProject(context.Background(), os.Getenv("SAN_PROJECT_TEST_HOME"), os.Getenv("SAN_PROJECT_TEST_CWD"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("SAN_PROJECT_TEST_RESULT"), []byte(p.ID), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	home, root, results := t.TempDir(), t.TempDir(), t.TempDir()
	commands := make([]*exec.Cmd, 6)
	for i := range commands {
		result := filepath.Join(results, string(rune('a'+i)))
		cmd := exec.Command(os.Args[0], "-test.run=^TestProjectProcessRegistration$")
		cmd.Env = append(os.Environ(), "SAN_PROJECT_TEST_CHILD=1", "SAN_PROJECT_TEST_HOME="+home, "SAN_PROJECT_TEST_CWD="+root, "SAN_PROJECT_TEST_RESULT="+result)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands[i] = cmd
	}
	var first string
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(results, string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = string(data)
		}
		if string(data) != first {
			t.Fatal("duplicate cross-process registration")
		}
	}
}
func TestProjectMissingMainCheckout(t *testing.T) {
	home, base := t.TempDir(), t.TempDir()
	root := filepath.Join(base, "main")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	projectTestGit(t, root, "init")
	projectTestGit(t, root, "commit", "--allow-empty", "-m", "initial")
	linked := filepath.Join(base, "linked")
	projectTestGit(t, root, "worktree", "add", "-b", "linked", linked)
	// Keep common metadata reachable while removing only the advertised source.
	// Worktree administration explicitly retains the original checkout location.
	common := filepath.Join(base, "common")
	if err := os.Rename(filepath.Join(root, ".git"), common); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+filepath.Join(common, "worktrees", "linked")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	p := projectTestResolve(t, home, linked)
	if p.Kind != "git" {
		t.Fatal("missing main changed Git identity")
	}
}

func TestProjectWithoutGit(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("PATH", t.TempDir())
	p := projectTestResolve(t, home, root)
	if p.Kind != "directory" {
		t.Fatal("nonGit failed without executable")
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProject(context.Background(), home, root); err == nil {
		t.Fatal("Git marker ignored without executable")
	}
}

func TestProjectCanceledRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveProject(ctx, t.TempDir(), t.TempDir()); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestProjectIDIsDerivedFromPlaceNotFromRegistry(t *testing.T) {
	root := t.TempDir()
	a := projectTestResolve(t, t.TempDir(), root)
	b := projectTestResolve(t, t.TempDir(), root)
	if a.ID != b.ID || a.ID != deriveProjectID("directory", a.Key) {
		t.Fatalf("same place, different homes: %s %s", a.ID, b.ID)
	}
	if deriveProjectID("git", a.Key) == a.ID {
		t.Fatal("kind is part of identity")
	}
	// project.json describes the project; hand-editing or deleting it changes nothing.
	path := filepath.Join(a.Root, "project.json")
	var saved projectFile
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Kind != "directory" || saved.Key != a.Key || saved.Name != filepath.Base(a.Workspace) {
		t.Fatalf("description: %+v %v", saved, err)
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(a.Root)))
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := projectTestResolve(t, home, root); c.ID != a.ID {
		t.Fatal("project.json decided identity")
	}
	if data, _ := os.ReadFile(path); string(data) == "garbage" {
		t.Fatal("description not refreshed")
	}
}
