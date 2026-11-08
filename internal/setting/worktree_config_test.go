package setting

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func configGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func configWorktree(t *testing.T) (string, string) {
	t.Helper()
	main := t.TempDir()
	configGit(t, main, "init", "-q")
	configGit(t, main, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	wt := filepath.Join(t.TempDir(), "branch")
	configGit(t, main, "worktree", "add", "-qb", "feature", wt)
	return main, wt
}

func TestWorktreeCheckoutSettingsAndWrites(t *testing.T) {
	home := trustHome(t)
	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"env":{"GLOBAL":"yes"}}`)
	main, wt := configWorktree(t)
	writeFile(t, filepath.Join(main, ".san", "settings.json"), `{"env":{"SOURCE":"main"},"persona":"original"}`)
	configGit(t, main, "add", ".san/settings.json")
	configGit(t, main, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "main settings")
	writeFile(t, filepath.Join(wt, ".san", "settings.json"), `{"env":{"SOURCE":"branch"},"persona":"branch"}`)
	configGit(t, wt, "add", ".san/settings.json")
	configGit(t, wt, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "branch settings")
	// Discovery itself never grants trust.
	d, err := LoadForCwd(wt)
	if err != nil {
		t.Fatal(err)
	}
	if d.Env["SOURCE"] != "" {
		t.Fatal("shared configuration bypassed trust")
	}
	if err := TrustWorkspace(wt); err != nil {
		t.Fatal(err)
	}
	d, err = LoadForCwd(wt)
	if err != nil {
		t.Fatal(err)
	}
	if d.Env["SOURCE"] != "branch" || d.Persona != "branch" || d.Env["GLOBAL"] != "yes" {
		t.Fatalf("checkout settings not respected: %+v", d)
	}
	sub := filepath.Join(wt, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SavePersonaAt(sub, "updated", ScopeProject); err != nil {
		t.Fatal(err)
	}
	s := New(d)
	if err := s.Reload(wt); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Persona != "updated" {
		t.Fatal("reload missed checkout write")
	}
	b, err := os.ReadFile(filepath.Join(main, ".san", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"env":{"SOURCE":"main"},"persona":"original"}` {
		t.Fatal("modified another checkout's settings")
	}
	if _, err := os.Stat(filepath.Join(sub, ".san", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("project write did not stay at checkout root")
	}
	writeFile(t, filepath.Join(wt, ".san", "settings.local.json"), `{"env":{"ONLY_TARGET":"yes"}}`)
	d, _ = LoadForCwd(main)
	if d.Env["ONLY_TARGET"] != "" {
		t.Fatal("local override leaked to main")
	}
	d, _ = LoadForCwd(wt)
	if d.Env["ONLY_TARGET"] != "yes" {
		t.Fatal("target override missing")
	}
}

func TestWorktreeLocalPrecedenceAndTrust(t *testing.T) {
	trustHome(t)
	main, wt := configWorktree(t)
	writeFile(t, filepath.Join(wt, ".san", "settings.local.json"), `{"env":{"TRACKED":"blocked"},"permissions":{"deny":["Read(secret)"]}}`)
	configGit(t, wt, "add", ".san/settings.local.json")
	writeFile(t, filepath.Join(main, ".claude", "settings.local.json"), `{"env":{"ORDER":"compat"}}`)
	writeFile(t, filepath.Join(main, ".san", "settings.local.json"), `{"env":{"ORDER":"main-local"}}`)
	d, err := LoadForCwd(wt)
	if err != nil {
		t.Fatal(err)
	}
	if d.Env["TRACKED"] != "" || d.Env["ORDER"] != "main-local" || len(d.Permissions.Deny) != 1 {
		t.Fatalf("local eligibility/precedence changed: env=%v deny=%v", d.Env, d.Permissions.Deny)
	}
	writeFile(t, filepath.Join(wt, ".san", "settings.local.json"), `{"env":{"ORDER":"target-local"}}`)
	if err := TrustWorkspace(wt); err != nil {
		t.Fatal(err)
	}
	d, err = LoadForCwd(wt)
	if err != nil {
		t.Fatal(err)
	}
	if d.Env["ORDER"] != "main-local" {
		t.Fatal("lost legacy main-local precedence")
	}
}

func TestUnavailableMainConfigDoesNotBlockCheckout(t *testing.T) {
	trustHome(t)
	main, wt := configWorktree(t)
	writeFile(t, filepath.Join(wt, ".san", "settings.json"), `{"persona":"branch"}`)
	// A non-directory main config path cannot be read; it is not our source.
	writeFile(t, filepath.Join(main, ".san"), "unavailable")
	if err := TrustWorkspace(wt); err != nil {
		t.Fatal(err)
	}
	d, err := LoadForCwd(wt)
	if err != nil || d.Persona != "branch" {
		t.Fatalf("checkout settings blocked: %v %v", d, err)
	}
	if err := SavePersonaAt(wt, "updated", ScopeProject); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeMissingSettingsDoesNotFallBackToMain(t *testing.T) {
	trustHome(t)
	main, wt := configWorktree(t)
	writeFile(t, filepath.Join(main, ".san", "settings.json"), `{"persona":"main"}`)
	if err := TrustWorkspace(wt); err != nil {
		t.Fatal(err)
	}
	d, err := LoadForCwd(wt)
	if err != nil || d.Persona != "" {
		t.Fatalf("inherited main project file: %v %v", d, err)
	}
}

func TestNonGitSettingsExactCwd(t *testing.T) {
	trustHome(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(parent, ".san", "settings.json"), `{"env":{"ANCESTOR":"no"}}`)
	TrustWorkspaceForSession(child)
	d, err := LoadForCwd(child)
	if err != nil {
		t.Fatal(err)
	}
	if d.Env["ANCESTOR"] != "" {
		t.Fatal("inherited non-Git ancestor")
	}
}
