//go:build windows

package fs

import (
	"bufio"
	"fmt"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
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

func outputReader(r io.Reader) io.Reader {
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == 0 {
		cp = windows.GetACP()
	}
	return &codePageReader{reader: bufio.NewReader(r), cp: cp}
}

type codePageReader struct {
	reader  *bufio.Reader
	decoded io.Reader
	cp      uint32
}

func (r *codePageReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.decoded != nil {
		return r.decoded.Read(p)
	}
	if _, err := r.reader.Peek(1); err != nil {
		return 0, err
	}
	b, _ := r.reader.Peek(r.reader.Buffered())
	n := 0
	for n < len(b) && b[n] < utf8.RuneSelf {
		n++
	}
	if n > 0 {
		return r.reader.Read(p[:min(n, len(p))])
	}
	// Wait only for enough bytes to distinguish UTF-8 from the console code page.
	b, err := r.reader.Peek(utf8.UTFMax)
	b, _ = r.reader.Peek(r.reader.Buffered())
	validUTF8 := utf8.Valid(b)
	if !validUTF8 && err == nil {
		for trim := 1; trim < utf8.UTFMax; trim++ {
			if utf8.Valid(b[:len(b)-trim]) {
				validUTF8 = true
				break
			}
		}
	}
	r.decoded = r.reader
	// shortcut: infer encoding from the first non-ASCII block; use explicit encoding for mixed-encoding commands.
	if r.cp != 65001 && !validUTF8 {
		for _, name := range []string{fmt.Sprintf("windows-%d", r.cp), fmt.Sprintf("cp%d", r.cp), fmt.Sprintf("IBM%d", r.cp)} {
			if enc, err := ianaindex.IANA.Encoding(name); err == nil && enc != nil {
				r.decoded = transform.NewReader(r.reader, enc.NewDecoder())
				break
			}
		}
	}
	return r.decoded.Read(p)
}
