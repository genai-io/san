package kit

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestEditorCommandSkipsMissingEditorAndKeepsArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-ins for editors")
	}
	bin := t.TempDir()
	for _, name := range []string{"code", "nano", "my editor"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	t.Setenv("EDITOR", "nvim") // not installed
	t.Setenv("VISUAL", "code -w")
	if got := EditorCommand("f.md").Args; !slices.Equal(got, []string{"code", "-w", "f.md"}) {
		t.Errorf("args = %v, want VISUAL with its flag", got)
	}

	t.Setenv("VISUAL", filepath.Join(bin, "my editor")) // a path with a space, as on Windows
	if got := EditorCommand("f.md").Args; !slices.Equal(got, []string{filepath.Join(bin, "my editor"), "f.md"}) {
		t.Errorf("args = %v, want the spaced path kept whole", got)
	}

	t.Setenv("VISUAL", "")
	if got := EditorCommand("f.md").Args; !slices.Equal(got, []string{"nano", "f.md"}) {
		t.Errorf("args = %v, want the installed fallback", got)
	}
}
