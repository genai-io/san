package perm

import (
	"strings"
	"testing"
)

// Lines parsed from a Windows file's diff carry no \r: it is the file's line
// ending, not text to show.
func TestParseUnifiedDiffDropsCarriageReturns(t *testing.T) {
	d := GenerateDiff("f.go", "a\r\nold\r\nc\r\n", "a\r\nnew\r\nc\r\n")
	if d.AddedCount != 1 || d.RemovedCount != 1 {
		t.Fatalf("added=%d removed=%d, want 1/1", d.AddedCount, d.RemovedCount)
	}
	for _, line := range d.Lines {
		if strings.Contains(line.Content, "\r") {
			t.Errorf("%v line %q keeps a carriage return", line.Type, line.Content)
		}
	}
}
