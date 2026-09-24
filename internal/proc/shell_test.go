package proc

import (
	"errors"
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
