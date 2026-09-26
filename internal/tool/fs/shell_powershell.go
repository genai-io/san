package fs

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"unicode/utf16"
)

// powerShellScript wraps a command for a non-interactive run: UTF-8 output,
// the final directory written back when San asked for it (cwdFileEnvVar is
// set), and the last native program's exit code as the process's own.
func powerShellScript(command string) string {
	return "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\n" +
		"$OutputEncoding = [System.Text.Encoding]::UTF8\n" +
		"try {\n" + command + "\n} finally {\n" +
		"  if ($env:" + cwdFileEnvVar + ") { [System.IO.File]::WriteAllText($env:" + cwdFileEnvVar + ", (Get-Location).ProviderPath) }\n" +
		"}\n" +
		"if ($LASTEXITCODE) { exit $LASTEXITCODE }\n"
}

// maxEncodedCommandLen keeps -EncodedCommand clear of Windows' ~32K-character
// command-line cap, leaving room for the executable path and other flags.
const maxEncodedCommandLen = 30000

// powerShellArgs runs script through -EncodedCommand: base64 of its UTF-16LE
// bytes, so no quote or backslash in it is ever re-read by a command line. A
// script too long for that runs from a temp file that deletes itself.
func powerShellArgs(script string) []string {
	flags := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass"}
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	if len(encoded) > maxEncodedCommandLen {
		if path, err := powerShellFile(script); err == nil {
			return append(flags, "-File", path)
		}
		// Unwritable temp dir: the encoded form still fails, but with Windows'
		// own "command line too long" error.
	}
	return append(flags, "-EncodedCommand", encoded)
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
