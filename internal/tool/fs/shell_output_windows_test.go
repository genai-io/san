//go:build windows

package fs

import (
	"bufio"
	"io"
	"strings"
	"testing"
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
	for _, tc := range []struct{ body, want string }{
		{string([]byte{0xD6, 0xD0, 0xCE, 0xC4}), "中文"},
		{"中文", "中文"},
		{string([]byte{0xC2, 0xA9, 0xD6, 0xD0, 0xCE, 0xC4}), "漏中文"},
	} {
		r := &codePageReader{reader: bufio.NewReader(strings.NewReader(prefix + tc.body)), cp: 936}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != prefix+tc.want {
			t.Fatalf("stream decode failed: length = %d, err = %v", len(got), err)
		}
	}
}
