package fs

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestWriteReceiptCountsLogicalLines(t *testing.T) {
	for _, tc := range []struct {
		content string
		lines   int
	}{{"", 0}, {"x", 1}, {"x\n", 1}, {"x\n\n", 2}} {
		r := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{"file_path": "sample", "content": tc.content}, t.TempDir())
		if !r.Success || r.Metadata.LineCount != tc.lines || !strings.Contains(r.Output, fmt.Sprintf("(%d line", tc.lines)) {
			t.Errorf("%q: receipt %q metadata %d error %q", tc.content, r.Output, r.Metadata.LineCount, r.Error)
		}
	}
}
