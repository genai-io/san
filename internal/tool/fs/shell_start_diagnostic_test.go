package fs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnoseShellStartPaths(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, vanished := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "disappeared"}[vanished], func(t *testing.T) {
			cwd := filepath.Join(t.TempDir(), "cwd")
			if vanished {
				if err := os.Mkdir(cwd, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(cwd); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(context.Background(), executable)
			cmd.Dir = cwd
			startErr := cmd.Start()
			if startErr == nil {
				_ = cmd.Wait()
				t.Fatal("expected failed start")
			}
			got := diagnoseShellStart(cmd, startErr)
			if !errors.Is(got, startErr) || !strings.Contains(got.Error(), "working directory") || strings.Contains(got.Error(), "shell executable") {
				t.Fatalf("unexpected diagnostic: %v", got)
			}
		})
	}
	t.Run("missing shell", func(t *testing.T) {
		cmd := exec.Command(filepath.Join(t.TempDir(), "missing-shell"))
		cmd.Dir = t.TempDir()
		startErr := cmd.Start()
		got := diagnoseShellStart(cmd, startErr)
		if got == nil || !errors.Is(got, startErr) || !strings.Contains(got.Error(), "shell executable") || strings.Contains(got.Error(), "working directory") {
			t.Fatalf("unexpected diagnostic: %v", got)
		}
	})
	t.Run("unrelated error", func(t *testing.T) {
		cmd := exec.Command(executable)
		cmd.Dir = t.TempDir()
		want := errors.New("start failure")
		if got := diagnoseShellStart(cmd, want); got != want {
			t.Fatalf("unexpected diagnostic: %v", got)
		}
		if got := diagnoseShellStart(cmd, nil); got != nil {
			t.Fatalf("successful start changed: %v", got)
		}
	})
}
