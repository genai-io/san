//go:build windows

package fs

import (
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// decodeOutput turns a command's output into UTF-8. What cannot be switched
// to UTF-8 — PowerShell in Constrained Language Mode, cmd, most native
// programs — writes the console's code page (GBK on Chinese Windows), which
// read as UTF-8 is mojibake. Without a console, the ANSI code page stands in.
func decodeOutput(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == 0 {
		cp = windows.GetACP()
	}
	return decodeCodePage(b, cp)
}

// decodeCodePage decodes b from Windows code page cp, or returns it as is
// when the conversion fails.
func decodeCodePage(b []byte, cp uint32) string {
	if len(b) == 0 || cp == 65001 {
		return string(b)
	}
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return string(b)
	}
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return string(b)
	}
	return string(utf16.Decode(u))
}
