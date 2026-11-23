package setting

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
)

func memoryTestGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable:", err)
	}
	if out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", cwd}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestMemoryStoreFor(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	memoryTestGit(t, repo, "init", "-q")
	def := system.MemoryStore(system.AutoMemoryDir(repo))
	if got := MemoryStoreFor(repo); got != def {
		t.Fatalf("default: got %q, want %q", got, def)
	}
	oldUserDir := filepath.Join(home, "notes")
	writeFile(t, filepath.Join(oldUserDir, "MEMORY.md"), "Legacy notes")
	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "~/notes"}`)
	writeFile(t, filepath.Join(repo, ".san", "settings.json"), `{"autoMemoryDirectory": "/tmp/untrusted"}`)
	if err := TrustWorkspace(repo); err != nil {
		t.Fatal(err)
	}
	if got := MemoryStoreFor(repo); got != def {
		t.Fatalf("global or shared settings redirected memory: %q", got)
	}
	local := filepath.Join(repo, ".san", "settings.local.json")
	for _, bad := range []string{"relative/dir", "/", "~/../etc"} {
		writeFile(t, local, `{"autoMemoryDirectory": "`+bad+`"}`)
		if got := MemoryStoreFor(repo); got != def {
			t.Errorf("accepted invalid override %q: %q", bad, got)
		}
	}
	writeFile(t, local, `{"autoMemoryDirectory": "/abs/local"}`)
	if got := MemoryStoreFor(repo); got != "/abs/local" {
		t.Errorf("untracked local override ignored: %q", got)
	}
	memoryTestGit(t, repo, "add", "-f", local)
	if got := MemoryStoreFor(repo); got != def {
		t.Errorf("tracked local settings redirected memory: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(oldUserDir, "MEMORY.md")); err != nil || string(got) != "Legacy notes" {
		t.Errorf("legacy user memory was modified: %q, %v", got, err)
	}
}

func TestSaveMemoryDirectorySharesOneStoreAcrossWorktrees(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	memoryTestGit(t, repo, "init", "-q")
	memoryTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	wt := filepath.Join(t.TempDir(), "linked")
	memoryTestGit(t, repo, "worktree", "add", "-q", "--detach", wt)
	sub := filepath.Join(wt, "nested")
	writeFile(t, filepath.Join(sub, ".san", "settings.local.json"), `{"autoMemoryDirectory": "/abs/nested"}`)
	writeFile(t, filepath.Join(wt, ".san", "settings.local.json"), `{"autoMemoryDirectory": "/abs/worktree"}`)
	def := system.MemoryStore(system.AutoMemoryDir(repo))
	for _, cwd := range []string{repo, wt, sub} {
		if got := MemoryStoreFor(cwd); got != def {
			t.Fatalf("%s does not share default memory: %q", cwd, got)
		}
	}
	other := t.TempDir()
	memoryTestGit(t, other, "init", "-q")
	if err := SaveMemoryDirectory(sub, "~/repo-notes"); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{repo, wt, sub} {
		if got := MemoryStoreFor(cwd); string(got) != filepath.Join(home, "repo-notes") {
			t.Fatalf("%s does not share the override: %q", cwd, got)
		}
	}
	if got := MemoryStoreFor(other); got == def || string(got) != system.AutoMemoryDir(other) {
		t.Fatalf("another repository shared memory: %q", got)
	}
	if err := SaveMemoryDirectory(wt, ""); err != nil || MemoryStoreFor(repo) != def {
		t.Errorf("clearing should restore default: %v", err)
	}
	if SaveMemoryDirectory(wt, "relative") == nil {
		t.Error("accepted a relative path")
	}
	writeFile(t, filepath.Join(repo, ".san", "settings.local.json"), `{}`)
	memoryTestGit(t, repo, "add", "-f", ".san/settings.local.json")
	if SaveMemoryDirectory(wt, "/abs/other") == nil {
		t.Error("accepted a tracked settings.local.json")
	}
}

// Saving another setting keeps a moved store where it is.
func TestOtherSavesKeepMemoryDirectory(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	memoryTestGit(t, repo, "init", "-q")
	if err := SaveMemoryDirectory(repo, "~/notes"); err != nil {
		t.Fatal(err)
	}
	loader := NewLoaderForCwd(repo)
	if err := loader.saveToFile(memoryDirectorySettings(repo), &Data{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if got := MemoryStoreFor(repo); string(got) != filepath.Join(home, "notes") {
		t.Errorf("a later save dropped autoMemoryDirectory: %q", got)
	}
}

// A one-setting save writes that setting, not empty blocks of the others.
func TestSaveWritesNoEmptyBlocks(t *testing.T) {
	home := trustHome(t)
	if err := SaveAutoMemory(t.TempDir(), false, ScopeUser); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".san", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"permissions"`, `"selfLearn"`, `"autoPilot"`, `"steers"`} {
		if strings.Contains(string(got), key) {
			t.Errorf("an empty %s block was written:\n%s", key, got)
		}
	}
}
