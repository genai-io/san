package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAdaptiveShellSelection(t *testing.T) {
	dir := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	fixture := func(name string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(dir, name+suffix)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	bash := fixture("bash", 0755)
	zsh := fixture("zsh", 0755)
	pwsh := fixture("pwsh", 0755)
	ps := fixture("powershell", 0755)
	fish := fixture("fish", 0755)
	t.Setenv("PATH", dir)
	missing := filepath.Join(dir, "missing", "zsh"+suffix)
	cases := []struct {
		name, override, pref, platform string
		kind                           ShellKind
		path                           string
		fallback, fail                 bool
	}{
		{"unix default", "", "", "linux", ShellBash, bash, false, false},
		{"bash preference", "", bash, "linux", ShellBash, bash, false, false},
		{"zsh preference", "", zsh, "darwin", ShellZsh, zsh, false, false},
		{"override wins", "bash", zsh, "linux", ShellBash, bash, false, false},
		{"uppercase bash override", "BASH", zsh, "linux", ShellBash, bash, false, false},
		{"uppercase zsh override", "ZSH", fish, "linux", ShellZsh, zsh, false, false},
		{"uppercase pwsh override", "PWSH", "", "windows", ShellPowerShell, pwsh, false, false},
		{"zsh override", "zsh", fish, "linux", ShellZsh, zsh, false, false},
		{"fish fallback", "", fish, "linux", ShellBash, bash, true, false},
		{"missing fallback", "", missing, "linux", ShellBash, bash, true, false},
		{"powershell preference unsupported", "", pwsh, "linux", ShellBash, bash, true, false},
		{"invalid override", fish, zsh, "linux", "", "", false, true},
		{"missing override", missing, zsh, "linux", "", "", false, true},
		{"windows ignores SHELL", "", zsh, "windows", ShellPowerShell, pwsh, false, false},
		{"windows explicit zsh", zsh, "", "windows", "", "", false, true},
		{"windows named zsh", "ZSH", "", "windows", "", "", false, true},
		{"powershell selection", "powershell", "", "windows", ShellPowerShell, pwsh, false, false},
		{"explicit powershell path", ps, "", "linux", ShellPowerShell, ps, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveShellForPlatform(tc.override, tc.pref, tc.platform)
			if tc.fail {
				if err == nil {
					t.Fatalf("accepted override %q", tc.override)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind || got.Path != tc.path {
				t.Fatalf("got %+v, want %s %s", got, tc.kind, tc.path)
			}
			if (got.FallbackReason != "") != tc.fallback {
				t.Fatalf("fallback reason = %q", got.FallbackReason)
			}
			if tc.fallback && !strings.Contains(got.FallbackReason, fmt.Sprintf("%q", tc.pref)) {
				t.Fatalf("unquoted preference: %s", got.FallbackReason)
			}
		})
	}
	if err := os.Remove(pwsh); err != nil {
		t.Fatal(err)
	}
	got, err := resolveShellForPlatform("", fish, "windows")
	if err != nil || got.Path != ps {
		t.Fatalf("Windows PowerShell fallback = %+v, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(zsh, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveShellForPlatform("", zsh, "linux")
		if err != nil || got.Path != bash || got.FallbackReason == "" {
			t.Fatalf("nonexecutable preference = %+v, %v", got, err)
		}
		if _, err := resolveShellForPlatform(zsh, "", "linux"); err == nil {
			t.Fatal("accepted nonexecutable override")
		}
	}
}

func TestZshLabelsAndSecondShell(t *testing.T) {
	shell := Shell{Kind: ShellZsh, Path: "/bin/zsh"}
	if shell.String() != "zsh" || shell.Kind.ToolName() != "Bash" {
		t.Fatalf("zsh labels: %s, %s", shell.String(), shell.Kind.ToolName())
	}
	second, ok := secondShell(shell, "bash", "pwsh")
	if !ok || second.Kind != ShellPowerShell {
		t.Fatalf("second shell = %+v, %v", second, ok)
	}
}
