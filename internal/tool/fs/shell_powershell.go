package fs

import (
	"encoding/base64"
	"encoding/binary"
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

// powerShellArgs runs script through -EncodedCommand: base64 of its UTF-16LE
// bytes, so no quote or backslash in it is ever re-read by a command line.
// ponytail: Windows caps a command line near 32K characters, which this
// encoding reaches at about 12K of script; write longer ones to a file first.
func powerShellArgs(script string) []string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	return []string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(raw),
	}
}
