package fs

import (
	"os"
	"unicode/utf16"
)

// powerShellScript wraps a command for a non-interactive run: UTF-8 output,
// no progress records, the final directory written back when San asked for it
// (cwdFileEnvVar is set), and the last native program's exit code as the
// process's own. It uses only cmdlets and variables, so it also runs where
// policy locks PowerShell into Constrained Language Mode; there the console
// encoding cannot be set, output keeps the system code page, and
// decodeOutput reads it.
func powerShellScript(command string) string {
	return "$ProgressPreference = 'SilentlyContinue'\n" +
		"try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}\n" +
		"$OutputEncoding = [System.Text.Encoding]::UTF8\n" +
		"try {\n" + command + "\n} finally {\n" +
		"  if ($env:" + cwdFileEnvVar + ") { Set-Content -LiteralPath $env:" + cwdFileEnvVar + " -Value (Get-Location).ProviderPath -Encoding UTF8 -NoNewline }\n" +
		"}\n" +
		"if ($LASTEXITCODE) { exit $LASTEXITCODE }\n"
}

// scriptEnvVar carries the script to PowerShell. Windows caps one
// environment variable at 32767 UTF-16 units; maxScriptEnvLen leaves room for
// the name.
const (
	scriptEnvVar    = "SAN_POWERSHELL_SCRIPT"
	maxScriptEnvLen = 32000
)

// powerShellArgs runs script with a fixed Invoke-Expression that reads it from
// scriptEnvVar, so no quote or backslash in it is re-read by a command line;
// env is what to add to the process environment. Unlike -EncodedCommand, this
// keeps Windows PowerShell 5.1's errors plain text on a redirected stderr
// instead of CLIXML prefixed with the whole script. A script too long for an
// environment variable runs from a temp file that deletes itself.
func powerShellArgs(script string) (args, env []string) {
	flags := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-OutputFormat", "Text"}
	if len(utf16.Encode([]rune(script))) > maxScriptEnvLen {
		if path, err := powerShellFile(script); err == nil {
			return append(flags, "-File", path), nil
		}
		// Unwritable temp dir: the variable is still too long, and starting
		// the process reports it.
	}
	return append(flags, "-Command", "Invoke-Expression $env:"+scriptEnvVar), []string{scriptEnvVar + "=" + script}
}

// powerShellFile saves script as a temp .ps1 whose first line removes the
// file; PowerShell has parsed the whole file by then. The UTF-8 BOM makes
// Windows PowerShell 5.1 read it as UTF-8 rather than the ANSI code page.
func powerShellFile(script string) (string, error) {
	f, err := os.CreateTemp("", "san-*.ps1")
	if err != nil {
		return "", err
	}
	body := "\uFEFFRemove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue\n" + script
	_, err = f.WriteString(body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
