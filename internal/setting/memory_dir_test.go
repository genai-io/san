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

	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "~/notes/memory"}`)
	if got, want := AutoMemoryDir(repo), filepath.Join(home, "notes", "memory"); got != want {
		t.Errorf("user setting: got %s, want %s", got, want)
	}

	local := filepath.Join(repo, ".san", "settings.local.json")
	writeFile(t, local, `{"autoMemoryDirectory": "/abs/local"}`)
	if got := AutoMemoryDir(repo); got != "/abs/local" {
		t.Errorf("untracked local setting: got %s", got)
	}
	if err := exec.Command("git", "-C", repo, "add", "-f", local).Run(); err != nil {
		t.Fatal(err)
	}
	if got := AutoMemoryDir(repo); got != filepath.Join(home, "notes", "memory") {
		t.Errorf("a committed settings.local.json must not count: got %s", got)
	}

	writeFile(t, filepath.Join(home, ".san", "settings.json"), `{"autoMemoryDirectory": "relative/dir"}`)
	if got := AutoMemoryDir(repo); got != def {
		t.Errorf("relative path should fall back to the default, got %s", got)
	}
}
