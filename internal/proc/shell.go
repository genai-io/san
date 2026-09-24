package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ShellKind is the language a shell speaks, which decides the tool the model
// is given and the syntax it writes.
type ShellKind string

const (
	ShellBash       ShellKind = "bash"
	ShellPowerShell ShellKind = "powershell"
)

// ToolName is the name of the tool that runs this shell's commands, and so
// the syntax the model is told to write.
func (k ShellKind) ToolName() string {
	if k == ShellPowerShell {
		return "PowerShell"
	}
	return "Bash"
}

// Shell is the interpreter shell commands run under.
type Shell struct {
	Kind ShellKind
	Path string
}

// String names the shell the way a person would, including which PowerShell:
// Windows PowerShell 5.1 lacks && and ||, which pwsh has.
func (s Shell) String() string {
	if s.Kind != ShellPowerShell {
		return string(s.Kind)
	}
	if strings.HasPrefix(strings.ToLower(filepath.Base(s.Path)), "pwsh") {
		return "PowerShell 7 (pwsh)"
	}
	return "Windows PowerShell 5.1"
}

// DefaultShell is the shell commands run under: bash on Unix; on Windows Git
// for Windows' bash when installed, else PowerShell. SAN_SHELL overrides it
// with "bash", "powershell", or a path to either.
func DefaultShell() (Shell, error) { return defaultShell() }

var defaultShell = sync.OnceValues(func() (Shell, error) {
	return resolveShell(strings.TrimSpace(os.Getenv("SAN_SHELL")))
})

func resolveShell(override string) (Shell, error) {
	switch strings.ToLower(override) {
	case "":
	case "bash":
		if path, ok := BashPath(); ok {
			return Shell{Kind: ShellBash, Path: path}, nil
		}
		return Shell{}, fmt.Errorf("SAN_SHELL=bash, but no bash was found")
	case "powershell", "pwsh":
		if path, ok := PowerShellPath(); ok {
			return Shell{Kind: ShellPowerShell, Path: path}, nil
		}
		return Shell{}, fmt.Errorf("SAN_SHELL=%s, but no PowerShell was found", override)
	default:
		if !fileExists(override) {
			return Shell{}, fmt.Errorf("SAN_SHELL=%s: no such file", override)
		}
		base := strings.ToLower(filepath.Base(override))
		if strings.HasPrefix(base, "pwsh") || strings.HasPrefix(base, "powershell") {
			return Shell{Kind: ShellPowerShell, Path: override}, nil
		}
		return Shell{Kind: ShellBash, Path: override}, nil
	}

	if path, ok := BashPath(); ok {
		return Shell{Kind: ShellBash, Path: path}, nil
	}
	if runtime.GOOS == "windows" {
		if path, ok := PowerShellPath(); ok {
			return Shell{Kind: ShellPowerShell, Path: path}, nil
		}
		return Shell{}, fmt.Errorf("no shell found: install Git for Windows, or make powershell.exe reachable on PATH")
	}
	return Shell{}, fmt.Errorf("bash not found on PATH")
}

// Shells are the shells commands can run under: DefaultShell, then — on a
// Windows with both Git Bash and PowerShell — the other one, which ships
// disabled for the user to turn on. Unix offers bash alone.
func Shells() []Shell {
	def, err := DefaultShell()
	if err != nil {
		return nil
	}
	if second, ok := SecondShell(); ok {
		return []Shell{def, second}
	}
	return []Shell{def}
}

// SecondShell is the shell offered beside DefaultShell, which ships disabled.
func SecondShell() (Shell, bool) {
	if runtime.GOOS != "windows" {
		return Shell{}, false
	}
	def, err := DefaultShell()
	if err != nil {
		return Shell{}, false
	}
	bash, _ := BashPath()
	powerShell, _ := PowerShellPath()
	return secondShell(def, bash, powerShell)
}

// secondShell is the other kind than def, when its interpreter was found.
func secondShell(def Shell, bash, powerShell string) (Shell, bool) {
	switch {
	case def.Kind == ShellBash && powerShell != "":
		return Shell{Kind: ShellPowerShell, Path: powerShell}, true
	case def.Kind == ShellPowerShell && bash != "":
		return Shell{Kind: ShellBash, Path: bash}, true
	}
	return Shell{}, false
}

// DefaultShellName names DefaultShell for a prompt, or "" when there is none.
func DefaultShellName() string {
	shell, err := DefaultShell()
	if err != nil {
		return ""
	}
	return shell.String()
}

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
