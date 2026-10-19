package setting

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core/system"
)

// autoMemoryDirectory moves the store of the file that sets it, and only the
// user's own files count: a committed project file, trusted or not, cannot
// move where memory writes land.
func TestMemoryStoresFor(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	if err := exec.Command("git", "init", "-q", repo).Run(); err != nil {
		t.Skip("git unavailable:", err)
	}
	def := system.MemoryStores{User: system.UserMemoryDir(home), Project: system.AutoMemoryDir(repo)}
	if got := MemoryStoresFor(repo); got != def {
		t.Fatalf("defaults: got %+v, want %+v", got, def)
	}

	writeFile(t, filepath.Join(repo, ".san", "settings.json"), `{"autoMemoryDirectory": "/tmp/evil"}`)
	if err := TrustWorkspace(repo); err != nil {
		t.Fatal(err)
	}
	if got := MemoryStoresFor(repo); got != def {
		t.Errorf("project settings moved memory: %+v", got)
	}

	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "~/notes"}`)
	if got, want := MemoryStoresFor(repo), (system.MemoryStores{User: filepath.Join(home, "notes"), Project: def.Project}); got != want {
		t.Errorf("user setting moves only the user store: got %+v, want %+v", got, want)
	}

	local := filepath.Join(repo, ".san", "settings.local.json")
	writeFile(t, local, `{"autoMemoryDirectory": "/abs/local"}`)
	if got := MemoryStoresFor(repo).Project; got != "/abs/local" {
		t.Errorf("untracked local setting moves the project store: got %s", got)
	}
	if err := exec.Command("git", "-C", repo, "add", "-f", local).Run(); err != nil {
		t.Fatal(err)
	}
	if got := MemoryStoresFor(repo).Project; got != def.Project {
		t.Errorf("a committed settings.local.json must not count: got %s", got)
	}

	for _, bad := range []string{"relative/dir", "/", "~/../etc"} {
		writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "`+bad+`"}`)
		if got := MemoryStoresFor(repo).User; got != def.User {
			t.Errorf("%q should fall back to the default, got %s", bad, got)
		}
	}
}

// /memory moves a store by writing autoMemoryDirectory where it counts, and
// refuses a tracked settings.local.json, where it would not.
func TestSaveMemoryDirectory(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	if err := exec.Command("git", "init", "-q", repo).Run(); err != nil {
		t.Skip("git unavailable:", err)
	}
	if err := SaveMemoryDirectory(ScopeUser, repo, "~/notes"); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemoryDirectory(ScopeProject, repo, "/abs/proj"); err != nil {
		t.Fatal(err)
	}
	want := system.MemoryStores{User: filepath.Join(home, "notes"), Project: "/abs/proj"}
	if got := MemoryStoresFor(repo); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if err := SaveMemoryDirectory(ScopeUser, repo, ""); err != nil || MemoryStoresFor(repo).User != system.UserMemoryDir(home) {
		t.Errorf("clearing should restore the default: err=%v", err)
	}
	if SaveMemoryDirectory(ScopeUser, repo, "relative") == nil {
		t.Error("a relative path should be refused")
	}
	if err := exec.Command("git", "-C", repo, "add", "-f", ".san/settings.local.json").Run(); err != nil {
		t.Fatal(err)
	}
	if SaveMemoryDirectory(ScopeProject, repo, "/abs/other") == nil {
		t.Error("a tracked settings.local.json should be refused")
	}
}

// Saving another setting keeps a moved store where it is.
func TestOtherSavesKeepMemoryDirectory(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	if err := SaveMemoryDirectory(ScopeUser, repo, "~/notes"); err != nil {
		t.Fatal(err)
	}
	if err := SaveAutoMemory(false, ScopeUser); err != nil {
		t.Fatal(err)
	}
	if err := NewLoader().SaveToUser(&Data{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if got := MemoryStoresFor(repo).User; got != filepath.Join(home, "notes") {
		t.Errorf("a later save dropped autoMemoryDirectory: user store = %s", got)
	}
}
