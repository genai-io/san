package setting

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// PowerShell permission classification.
//
// There is no PowerShell parser here, so every judgment is made on text and
// errs one way: a command is read-only only when it is plainly so, and it is
// destructive whenever any statement in it might be. Anything that can hide a
// second command — ; && || & | ( ) { } $ @ ` [ ] redirection, a newline — makes
// a command not "simple", and only a simple command is ever let through by a
// rule broader than the exact command approved.

// powerShellReadOnly are cmdlets, with their built-in aliases, that read and
// never write, run code, or reach the network. Lower-case.
var powerShellReadOnly = map[string]bool{
	"get-childitem": true, "gci": true, "ls": true, "dir": true,
	"get-content": true, "gc": true, "cat": true, "type": true,
	"get-item": true, "gi": true, "get-itemproperty": true, "gp": true,
	"get-location": true, "gl": true, "pwd": true,
	"test-path": true, "resolve-path": true, "rvpa": true, "split-path": true, "join-path": true,
	"select-string": true, "sls": true,
	"measure-object": true, "measure": true, "select-object": true, "select": true,
	"sort-object": true, "sort": true, "group-object": true, "group": true, "get-unique": true, "gu": true,
	"compare-object": true, "compare": true, "diff": true,
	"where-object": true, "where": true, "?": true,
	"format-list": true, "fl": true, "format-table": true, "ft": true, "format-wide": true, "fw": true,
	"out-string": true, "write-output": true, "echo": true, "write": true,
	"get-command": true, "gcm": true, "get-date": true, "get-filehash": true,
	"get-process": true, "gps": true, "ps": true, "get-service": true, "gsv": true,
	"get-psdrive": true, "gdr": true,
}

// powerShellReadOnlyNative are programs judged by the bash read-only
// classifier, which knows their read-only subcommands and unsafe flags.
var powerShellReadOnlyNative = map[string]bool{"git": true, "git.exe": true, "rg": true, "rg.exe": true}

var (
	// powerShellCompound matches what can chain, nest, redirect or substitute.
	powerShellCompound = regexp.MustCompile("[;&|<>(){}$@`\\[\\]\r\n]")
	// powerShellStatementBreak is everything that can start another command.
	powerShellStatementBreak = regexp.MustCompile("[;&|(){}\r\n]")
	// cmdSwitch is a cmd.exe switch such as /s or /q, as opposed to a path.
	cmdSwitch = regexp.MustCompile(`^/[A-Za-z]$`)
)

// isSimplePowerShell reports whether cmd is one command and nothing more.
func isSimplePowerShell(cmd string) bool {
	return strings.TrimSpace(cmd) != "" && !powerShellCompound.MatchString(cmd)
}

// unquote strips the quotes a word may be written in.
func unquote(word string) string { return strings.Trim(word, `'"`) }

// commandName is a word as a command name: unquoted, lower-case, no .exe.
func commandName(word string) string {
	return strings.TrimSuffix(strings.ToLower(unquote(word)), ".exe")
}

// IsReadOnlyShellCommand classifies a shell tool's command in that shell's
// own syntax.
func IsReadOnlyShellCommand(toolName, cmd string) bool {
	switch toolName {
	case "Bash":
		return IsReadOnlyBashCommand(cmd)
	case "PowerShell":
		return IsReadOnlyPowerShellCommand(cmd)
	}
	return false
}

// IsReadOnlyPowerShellCommand reports whether a PowerShell command provably
// only reads: one pipeline of simple read-only commands (cmdlets, or
// read-only git and rg), and no UNC path, which would hand the machine's
// credentials to whatever server it names.
func IsReadOnlyPowerShellCommand(cmd string) bool {
	// Every segment must be simple, so || (an empty segment) fails too.
	for _, segment := range strings.Split(cmd, "|") {
		if !isSimplePowerShell(segment) {
			return false
		}
		fields := strings.Fields(segment)
		for _, f := range fields {
			if word := unquote(f); strings.HasPrefix(word, `\\`) || strings.HasPrefix(word, "//") {
				return false
			}
		}
		name := strings.ToLower(fields[0])
		switch {
		case powerShellReadOnly[name]:
		case powerShellReadOnlyNative[name]:
			if !IsReadOnlyBashCommand(strings.Join(append([]string{strings.TrimSuffix(name, ".exe")}, fields[1:]...), " ")) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// powerShellStatements splits cmd at everything that can start another
// command, including inside a subexpression or script block. It over-splits
// on purpose: it is used to find a command anywhere in cmd, never to prove
// there is only one.
func powerShellStatements(cmd string) [][]string {
	var out [][]string
	for _, part := range powerShellStatementBreak.Split(cmd, -1) {
		fields := strings.Fields(part)
		// Strip the call and dot-source operators and the $ of $(...).
		for len(fields) > 0 && (fields[0] == "." || fields[0] == "$") {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			fields[0] = strings.ToLower(unquote(fields[0]))
			out = append(out, fields)
		}
	}
	return out
}

// powerShellRemoval are Remove-Item and its aliases, plus cmd's own.
var powerShellRemoval = map[string]bool{
	"remove-item": true, "ri": true, "rm": true, "del": true, "erase": true, "rd": true, "rmdir": true,
}

// powerShellStartProcess are Start-Process and its aliases; -Verb RunAs elevates.
var powerShellStartProcess = map[string]bool{"start-process": true, "saps": true, "start": true}

// powerShellDestructive are commands whose effect nothing brings back, or that
// escalate privilege or persist beyond the session, whatever their arguments.
// Names are as commandName gives them, so without .exe.
var powerShellDestructive = map[string]bool{
	"format-volume": true, "clear-disk": true, "initialize-disk": true, "remove-partition": true,
	"diskpart": true, "format": true, "format.com": true, "bcdedit": true,
	"stop-computer": true, "restart-computer": true,
	"register-scheduledtask": true, "schtasks": true, "new-service": true,
	"set-executionpolicy": true, "runas": true, "takeown": true,
}

// recursiveRemovals returns the arguments of every recursive removal in cmd.
// Names are looked for at every position, not only first, so wrapping the
// removal in another shell — cmd /c rd /s, pwsh -c "rm -r ..." — does not
// hide it.
func recursiveRemovals(cmd string) [][]string {
	var out [][]string
	for _, fields := range powerShellStatements(cmd) {
		for i, f := range fields {
			if powerShellRemoval[commandName(f)] && isRecursiveRemoval(fields[i+1:]) {
				out = append(out, fields[i+1:])
			}
		}
	}
	return out
}

// isRecursiveRemoval reads -Recurse in any abbreviation PowerShell accepts,
// and cmd's rd /s and del /s.
func isRecursiveRemoval(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		lower := strings.ToLower(unquote(a))
		return strings.HasPrefix(lower, "-r") || lower == "/s"
	})
}

// isDestructivePowerShellCommand reports whether any statement in cmd makes a
// recursive removal, elevates, or names a command in powerShellDestructive —
// at any position, as recursiveRemovals looks.
func isDestructivePowerShellCommand(cmd string) bool {
	if len(recursiveRemovals(cmd)) > 0 {
		return true
	}
	for _, fields := range powerShellStatements(cmd) {
		for i, f := range fields {
			name := commandName(f)
			if powerShellDestructive[name] {
				return true
			}
			if powerShellStartProcess[name] && slices.ContainsFunc(fields[i+1:], func(a string) bool {
				return strings.EqualFold(unquote(a), "runas")
			}) {
				return true
			}
		}
	}
	return false
}

// isPowerShellRootOrHomeRemoval is the circuit breaker's PowerShell half: a
// recursive removal aimed at a drive root or the home directory.
func isPowerShellRootOrHomeRemoval(cmd string) bool {
	for _, args := range recursiveRemovals(cmd) {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && !cmdSwitch.MatchString(a) && isPowerShellRootOrHome(a) {
				return true
			}
		}
	}
	return false
}

var driveRoot = regexp.MustCompile(`^[A-Za-z]:[\\/]?\*?$`)

func isPowerShellRootOrHome(target string) bool {
	t := strings.TrimSuffix(strings.TrimSuffix(unquote(target), `\*`), "/*")
	switch strings.ToLower(strings.TrimRight(t, `\/`)) {
	case "", "~", "$home", "${home}", "$env:userprofile", "$env:homedrive", "$env:systemdrive", "$env:systemroot", "$env:windir":
		return true
	}
	if driveRoot.MatchString(t) {
		return true
	}
	home, err := os.UserHomeDir()
	return err == nil && home != "" && strings.EqualFold(filepath.Clean(t), filepath.Clean(home))
}

// powerShellGitDiscarding reports a work-discarding git command anywhere in
// cmd — the recoverable tier, as for Bash.
func powerShellGitDiscarding(cmd string) bool {
	for _, fields := range powerShellStatements(cmd) {
		if commandName(fields[0]) == "git" && isGitDiscardingCommand(strings.Join(append([]string{"git"}, fields[1:]...), " ")) {
			return true
		}
	}
	return false
}

// normalizePowerShell is the command as a rule names it: whitespace collapsed.
func normalizePowerShell(cmd string) string {
	return strings.Join(strings.Fields(cmd), " ")
}

// matchPowerShellAllow reports whether an allow pattern covers a PowerShell
// call. A prefix covers a simple command only, so it can never wave through
// a chained one.
func matchPowerShellAllow(cmd, pattern string) bool {
	var statements [][]string
	if isSimplePowerShell(cmd) {
		statements = powerShellStatements(cmd)
	}
	return matchPowerShellRule(cmd, pattern, statements)
}

// matchPowerShellDeny reports whether a deny or ask pattern matches a
// PowerShell call: any statement anywhere in it is enough.
func matchPowerShellDeny(cmd, pattern string) bool {
	return matchPowerShellRule(cmd, pattern, powerShellStatements(cmd))
}

// matchPowerShellRule matches a bare shell name (the shell tool as a whole),
// the exact command, or "name:args-glob" against statements. A Bash pattern
// with arguments is bash syntax and never applies.
func matchPowerShellRule(cmd, pattern string, statements [][]string) bool {
	if isBareShellRule(pattern) {
		return true
	}
	toolName, arg := parseRule(pattern)
	if toolName != "PowerShell" {
		return false
	}
	if arg == normalizePowerShell(cmd) {
		return true
	}
	name, argsGlob, ok := strings.Cut(arg, ":")
	if !ok {
		return false
	}
	for _, fields := range statements {
		if strings.EqualFold(fields[0], name) && matchGlob(strings.Join(fields[1:], " "), argsGlob) {
			return true
		}
	}
	return false
}

// isBareShellRule reports a rule naming a shell tool with no pattern, which
// covers whichever shell tool the platform has.
func isBareShellRule(pattern string) bool {
	return pattern == "Bash" || pattern == "PowerShell"
}

// powerShellRiskyPrefixes are PowerShell commands that run other code or reach
// the network, so a prefix rule for them would approve far more than was
// asked; with dangerousPrefixes, they are never suggested.
var powerShellRiskyPrefixes = map[string]bool{
	"pwsh": true, "powershell": true, "cmd": true,
	"invoke-expression": true, "iex": true, "invoke-command": true, "icm": true,
	"start-process": true, "saps": true, "start": true,
	"invoke-webrequest": true, "iwr": true, "invoke-restmethod": true, "irm": true, "curl": true, "wget": true,
}

// suggestPowerShellRules proposes an allow rule for a simple command: its
// name and first argument as a prefix, like Bash's. Nothing for a compound
// command, or for one that removes, escalates, or runs other code.
func suggestPowerShellRules(cmd string) []string {
	if !isSimplePowerShell(cmd) || isDestructivePowerShellCommand(cmd) {
		return nil
	}
	fields := strings.Fields(cmd)
	name := fields[0]
	if lower := commandName(name); powerShellRemoval[lower] || powerShellRiskyPrefixes[lower] || dangerousPrefixes[lower] {
		return nil
	}
	if len(fields) > 1 {
		return []string{"PowerShell(" + name + ":" + fields[1] + " *)"}
	}
	return []string{"PowerShell(" + name + ":*)"}
}
