package proc

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The layouts Git for Windows is found in, and the WSL stubs that must never
// be taken for it. Paths are built with the host's separator so the search
// logic runs the same off Windows.
func TestFindGitBash(t *testing.T) {
	programFiles := filepath.Join("C:", "Program Files")
	installed := filepath.Join(programFiles, "Git", "bin", "bash.exe")
	system32Bash := filepath.Join("C:", "Windows", "System32", "bash.exe")

	cases := []struct {
		name   string
		onPath map[string]string
		env    map[string]string
		files  []string
		want   string
	}{
		{
			name:   "beside the git on PATH",
			onPath: map[string]string{"git": filepath.Join("D:", "tools", "Git", "cmd", "git.exe")},
			files:  []string{filepath.Join("D:", "tools", "Git", "bin", "bash.exe")},
			want:   filepath.Join("D:", "tools", "Git", "bin", "bash.exe"),
		},
		{
			name:  "the default install when git is not on PATH",
			env:   map[string]string{"ProgramFiles": programFiles},
			files: []string{installed},
			want:  installed,
		},
		{
			name:   "never the WSL launcher in System32",
			onPath: map[string]string{"bash": system32Bash},
			env:    map[string]string{"SystemRoot": filepath.Join("C:", "Windows")},
			want:   "",
		},
		{
			name:   "never the WindowsApps alias",
			onPath: map[string]string{"bash": filepath.Join("C:", "Users", "me", "AppData", "Local", "Microsoft", "WindowsApps", "bash.exe")},
			want:   "",
		},
		{
			name:   "a bash on PATH that is neither",
			onPath: map[string]string{"bash": filepath.Join("C:", "msys64", "usr", "bin", "bash.exe")},
			env:    map[string]string{"SystemRoot": filepath.Join("C:", "Windows")},
			want:   filepath.Join("C:", "msys64", "usr", "bin", "bash.exe"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookPath := func(name string) (string, error) {
				if path, ok := tc.onPath[name]; ok {
					return path, nil
				}
				return "", errors.New("not found")
			}
			getenv := func(key string) string { return tc.env[key] }
			exists := func(path string) bool { return slices.Contains(tc.files, path) }
			if got := findGitBash(lookPath, getenv, exists); got != tc.want {
				t.Errorf("findGitBash() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShellNamesWhichPowerShell(t *testing.T) {
	for path, want := range map[string]string{
		filepath.Join("C:", "Program Files", "PowerShell", "7", "pwsh.exe"):                       "PowerShell 7 (pwsh)",
		filepath.Join("C:", "Windows", "System32", "WindowsPowerShell", "v1.0", "powershell.exe"): "Windows PowerShell 5.1",
	} {
		if got := (Shell{Kind: ShellPowerShell, Path: path}).String(); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if got := (Shell{Kind: ShellBash, Path: "/bin/bash"}).String(); got != "bash" {
		t.Errorf("bash = %q", got)
	}
}

// SAN_SHELL names a shell by kind or by path; the kind of a path is read from
// its file name.
func TestResolveShellOverride(t *testing.T) {
	dir := t.TempDir()
	pwsh := filepath.Join(dir, "pwsh.exe")
	zsh := filepath.Join(dir, "zsh")
	for _, f := range []string{pwsh, zsh} {
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := resolveShell(pwsh); err != nil || got.Kind != ShellPowerShell || got.Path != pwsh {
		t.Errorf("resolveShell(%q) = %+v, %v", pwsh, got, err)
	}
	if got, err := resolveShell(zsh); err != nil || got.Kind != ShellBash {
		t.Errorf("resolveShell(%q) = %+v, %v", zsh, got, err)
	}
	if _, err := resolveShell(filepath.Join(dir, "missing")); err == nil {
		t.Error("a SAN_SHELL naming no file was accepted")
	}
}

// The second shell is the kind the default is not, and only when its
// interpreter was found.
func TestSecondShell(t *testing.T) {
	bash := Shell{Kind: ShellBash, Path: "bash.exe"}
	powerShell := Shell{Kind: ShellPowerShell, Path: "pwsh.exe"}
	cases := []struct {
		name, bashPath, psPath string
		def                    Shell
		want                   Shell
		ok                     bool
	}{
		{"default bash, PowerShell found", "bash.exe", "pwsh.exe", bash, powerShell, true},
		{"default PowerShell, bash found", "bash.exe", "pwsh.exe", powerShell, bash, true},
		{"default bash, no PowerShell", "bash.exe", "", bash, Shell{}, false},
	}
	for _, tc := range cases {
		got, ok := secondShell(tc.def, tc.bashPath, tc.psPath)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: secondShell = %+v, %v; want %+v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
