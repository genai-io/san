package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// BashPath returns the bash that shell commands run under, and false when
// there is none.
//
// On Unix it is whatever "bash" resolves to. On Windows it is Git for
// Windows' bash and never the WSL launcher in System32, which would run the
// command inside a Linux distribution whose paths do not match the host's.
func BashPath() (string, bool) {
	path := bashPath()
	return path, path != ""
}

// PowerShellPath returns PowerShell 7 (pwsh) when installed, else the Windows
// PowerShell every Windows ships with, and false when neither resolves.
func PowerShellPath() (string, bool) {
	path := powerShellPath()
	return path, path != ""
}

var powerShellPath = sync.OnceValue(func() string {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
})

var bashPath = sync.OnceValue(func() string {
	if runtime.GOOS != "windows" {
		path, _ := exec.LookPath("bash")
		return path
	}
	return findGitBash(exec.LookPath, os.Getenv, fileExists)
})

// findGitBash locates Git for Windows' bash.exe: beside the git on PATH, then
// in the default install locations, then a bash on PATH that is not a WSL
// launcher. Its lookups are parameters so the search is testable off Windows.
func findGitBash(lookPath func(string) (string, error), getenv func(string) string, exists func(string) bool) string {
	var candidates []string
	// git.exe lives in Git\cmd (installer) or Git\bin (portable); bash.exe
	// in Git\bin, with a copy in Git\usr\bin.
	if git, err := lookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git))
		candidates = append(candidates,
			filepath.Join(root, "bin", "bash.exe"),
			filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	installs := []string{getenv("ProgramFiles"), getenv("ProgramFiles(x86)")}
	if local := getenv("LocalAppData"); local != "" {
		installs = append(installs, filepath.Join(local, "Programs"))
	}
	for _, base := range installs {
		if base != "" {
			candidates = append(candidates, filepath.Join(base, "Git", "bin", "bash.exe"))
		}
	}
	for _, candidate := range candidates {
		if exists(candidate) {
			return candidate
		}
	}
	if bash, err := lookPath("bash"); err == nil && !isWSLLauncher(bash, getenv("SystemRoot")) {
		return bash
	}
	return ""
}

// isWSLLauncher reports whether path is one of the bash.exe stubs Windows
// installs for WSL: System32\bash.exe and the WindowsApps alias.
func isWSLLauncher(path, systemRoot string) bool {
	sep := string(filepath.Separator)
	lower := strings.ToLower(filepath.Clean(path))
	if systemRoot != "" && strings.HasPrefix(lower, strings.ToLower(filepath.Join(systemRoot, "System32"))+sep) {
		return true
	}
	return strings.Contains(lower, sep+"windowsapps"+sep)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
