//go:build windows

package fs

import "testing"

// GBK bytes decode to the text they spell, and UTF-8 output passes through.
func TestDecodeOutputReadsTheCodePage(t *testing.T) {
	if got := decodeCodePage([]byte{0xD6, 0xD0, 0xCE, 0xC4}, 936); got != "中文" {
		t.Errorf("CP936 decoded to %q", got)
	}
	if got := decodeOutput([]byte("已是 UTF-8")); got != "已是 UTF-8" {
		t.Errorf("UTF-8 changed to %q", got)
	}
}
