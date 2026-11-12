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
	ShellZsh        ShellKind = "zsh"
	ShellBash       ShellKind = "bash"
	ShellPowerShell ShellKind = "powershell"
)

// ToolName preserves Bash identifiers for both Unix interpreters.
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
	// FallbackReason explains why the user's SHELL preference was not used.
	FallbackReason string
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

// DefaultShell returns the cached interpreter selected by SAN_SHELL, then the
// supported Unix SHELL preference, then the platform default.
func DefaultShell() (Shell, error) { return defaultShell() }

var defaultShell = sync.OnceValues(func() (Shell, error) {
	return resolveShell(os.Getenv("SAN_SHELL"))
})

func resolveShell(override string) (Shell, error) {
	return resolveShellForPlatform(override, os.Getenv("SHELL"), runtime.GOOS)
}

// resolveShellForPlatform is uncached so callers can test environment selection.
func resolveShellForPlatform(override, preference, goos string) (Shell, error) {
	if override = strings.TrimSpace(override); override != "" {
		shell, err := resolveShellValue(override, goos)
		if err != nil {
			return Shell{}, fmt.Errorf("SAN_SHELL=%q: %w", override, err)
		}
		return shell, nil
	}
	var reason string
	if goos != "windows" && preference != "" {
		kind, ok := shellKind(preference)
		if ok && (kind == ShellBash || kind == ShellZsh) {
			if shell, err := resolveShellValue(preference, goos); err == nil {
				return shell, nil
			} else {
				reason = fmt.Sprintf("SHELL=%q is unavailable: %v; falling back to bash", preference, err)
			}
		} else {
			reason = fmt.Sprintf("SHELL=%q is unsupported; falling back to bash", preference)
		}
	}
	name := "bash"
	if goos == "windows" {
		name = "powershell"
	}
	shell, err := resolveShellValue(name, goos)
	if err != nil {
		return Shell{}, fmt.Errorf("default shell: %w", err)
	}
	shell.FallbackReason = reason
	return shell, nil
}

func shellKind(value string) (ShellKind, bool) {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(value)), ".exe")
	switch base {
	case "bash":
		return ShellBash, true
	case "zsh":
		return ShellZsh, true
	case "powershell", "pwsh":
		return ShellPowerShell, true
	}
	return "", false
}

func resolveShellValue(value, goos string) (Shell, error) {
	kind, ok := shellKind(value)
	if !ok {
		return Shell{}, fmt.Errorf("unsupported shell %q", value)
	}
	if kind == ShellZsh && goos == "windows" {
		return Shell{}, fmt.Errorf("zsh is unsupported on Windows")
	}
	var path string
	var err error
	switch strings.ToLower(value) {
	case "bash":
		if goos == "windows" {
			path = findGitBash(exec.LookPath, os.Getenv, executableExists)
		} else {
			path, err = exec.LookPath("bash")
		}
	case "zsh":
		path, err = exec.LookPath("zsh")
	case "powershell", "pwsh":
		for _, name := range []string{"pwsh", "powershell"} {
			path, err = exec.LookPath(name)
			if err == nil {
				break
			}
		}
	default:
		path, err = exec.LookPath(value)
	}
	if err != nil {
		return Shell{}, err
	}
	if path == "" {
		return Shell{}, fmt.Errorf("%s executable not found", value)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Shell{}, err
	}
	return Shell{Kind: kind, Path: path}, nil
}

func executableExists(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

// Shells are the shells commands can run under: DefaultShell, then — on a
// Windows that also has Git Bash — the other one, which ships disabled for the
// user to turn on in /tools. Unix offers only the selected interpreter.
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
	case (def.Kind == ShellBash || def.Kind == ShellZsh) && powerShell != "":
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
