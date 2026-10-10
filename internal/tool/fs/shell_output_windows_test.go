//go:build windows

package fs

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

// GBK bytes decode to the text they spell, and UTF-8 output passes through.
func TestDecodeOutputReadsTheCodePage(t *testing.T) {
	if got := decodeCodePage([]byte{0xD6, 0xD0, 0xCE, 0xC4}, 936); got != "中文" {
		t.Errorf("CP936 decoded to %q", got)
	}
	if got := decodeOutput([]byte("已是 UTF-8")); got != "已是 UTF-8" {
		t.Errorf("UTF-8 changed to %q", got)
	}
}

func TestCodePageReaderStreamsAfterASCIIAndKeepsUTF8(t *testing.T) {
	prefix := strings.Repeat("ascii\n", 10000)
	for _, tc := range []struct {
		cp         uint32
		body, want string
	}{
		{936, string([]byte{0xD6, 0xD0, 0xCE, 0xC4}), "中文"},
		{936, "中文", "中文"},
		{936, string([]byte{0xC2, 0xA9, 0xD6, 0xD0, 0xCE, 0xC4}), "漏中文"},
		{932, string([]byte{0x82, 0xA0}), "あ"},
		{949, string([]byte{0xC7, 0xD1, 0xB1, 0xDB}), "한글"},
		{950, string([]byte{0xA4, 0xA4, 0xA4, 0xE5}), "中文"},
	} {
		r := &codePageReader{reader: bufio.NewReader(strings.NewReader(prefix + tc.body)), cp: tc.cp}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != prefix+tc.want {
			t.Errorf("CP%d stream decode failed: length = %d, err = %v", tc.cp, len(got), err)
		}
	}
}

func TestCodePageReaderDoesNotWaitForExtraOutput(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		cp         uint32
	}{
		{"two-byte UTF-8", "é", 936},
		{"three-byte UTF-8", "中", 936},
		{"UTF-8 console", "中", 65001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, output := io.Pipe()
			defer input.Close()
			defer output.Close()
			r := &codePageReader{reader: bufio.NewReader(input), cp: tc.cp}
			done := make(chan struct{})
			var got string
			var readErr error
			go func() {
				buf := make([]byte, 16)
				n, err := r.Read(buf)
				got, readErr = string(buf[:n]), err
				close(done)
			}()
			for i := range len(tc.text) {
				if _, err := output.Write([]byte(tc.text[i : i+1])); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
				if got != tc.text || readErr != nil {
					t.Fatalf("read = %q, err = %v", got, readErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("complete character waits for more output or EOF")
			}
		})
	}
}
