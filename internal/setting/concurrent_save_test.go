package setting

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/infra/atomicfile"
)

func TestConcurrentUserPreferences(t *testing.T) {
	const helper = "SAN_TEST_CONCURRENT_PREFS"
	if os.Getenv(helper) == "1" {
		path := filepath.Join(NewLoader().userDir, "settings.json.lock")
		release, err := atomicfile.TryLock(path)
		if err == nil {
			release()
		}
		if !errors.Is(err, atomicfile.ErrLocked) {
			t.Fatalf("parent's settings transaction is not locked: %v", err)
		}
		fmt.Println("saving theme")
		if err := SaveTheme("dark"); err != nil {
			t.Fatal(err)
		}
		return
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cmd := exec.Command(os.Args[0], "-test.run=^TestConcurrentUserPreferences$")
	cmd.Env = append(os.Environ(), helper+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	err = updateSettingsFile(ScopeUser, func(d *Data) {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil || line != "saving theme\n" {
			_ = cmd.Wait()
			t.Fatalf("child: %q (%v)", line, err)
		}
		lines := 7
		d.BashPreviewLines = &lines
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader()
	d, err := loader.LoadFile(filepath.Join(loader.userDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Theme != "dark" || d.BashPreviewLines == nil || *d.BashPreviewLines != 7 {
		t.Errorf("lost preference: theme=%q bashPreviewLines=%v", d.Theme, d.BashPreviewLines)
	}
}
