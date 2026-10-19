package setting

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core/system"
)

// autoMemoryDirectory comes only from the user's own settings: a committed
// project file, trusted or not, cannot move where memory writes land.
func TestAutoMemoryDirOnlyFromUserSettings(t *testing.T) {
	home := trustHome(t)
	repo := t.TempDir()
	if err := exec.Command("git", "init", "-q", repo).Run(); err != nil {
		t.Skip("git unavailable:", err)
	}
	def := system.AutoMemoryDir(repo)

	writeFile(t, filepath.Join(repo, ".san", "settings.json"), `{"autoMemoryDirectory": "/tmp/evil"}`)
	if err := TrustWorkspace(repo); err != nil {
		t.Fatal(err)
	}
	if got := AutoMemoryDir(repo); got != def {
		t.Errorf("project settings moved memory to %s", got)
	}

	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "~/notes"}`)
	userDir := system.AutoMemoryDirIn(filepath.Join(home, "notes"), repo)
	if got := AutoMemoryDir(repo); got != userDir {
		t.Errorf("user setting: got %s, want %s", got, userDir)
	}
	if !IsAutoMemoryPath(filepath.Join(userDir, "MEMORY.md")) {
		t.Error("a file under the user's root is not seen as memory")
	}

	local := filepath.Join(repo, ".san", "settings.local.json")
	writeFile(t, local, `{"autoMemoryDirectory": "/abs/local"}`)
	if got := AutoMemoryDir(repo); got != system.AutoMemoryDirIn("/abs/local", repo) {
		t.Errorf("untracked local setting: got %s", got)
	}
	if err := exec.Command("git", "-C", repo, "add", "-f", local).Run(); err != nil {
		t.Fatal(err)
	}
	if got := AutoMemoryDir(repo); got != userDir {
		t.Errorf("a committed settings.local.json must not count: got %s", got)
	}

	for _, bad := range []string{"relative/dir", "/", "~/../etc"} {
		writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "`+bad+`"}`)
		if got := AutoMemoryDir(repo); got != def {
			t.Errorf("%q should fall back to the default, got %s", bad, got)
		}
	}
}

// One root holds a separate store per repository.
func TestAutoMemoryDirSeparatesRepositories(t *testing.T) {
	home := trustHome(t)
	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "~/notes"}`)
	a, b := t.TempDir(), t.TempDir()
	if AutoMemoryDir(a) == AutoMemoryDir(b) {
		t.Errorf("two repositories share %s", AutoMemoryDir(a))
	}
}
