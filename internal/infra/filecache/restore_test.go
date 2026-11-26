package filecache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoredLineCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.txt")
	if err := os.WriteFile(path, []byte("kept\n"+strings.Repeat("x", 21_000)), 0600); err != nil {
		t.Fatal(err)
	}
	cache := New()
	cache.Touch(path)
	files, tokens := cache.RestoreRecent()
	if len(files) != 1 || files[0].Content != "kept" || tokens != 1 {
		t.Fatal("expected only the short line to be restored")
	}
	if got := FormatRestoredFiles(files); !strings.Contains(got, " lines=1>\nkept\n</file>") {
		t.Errorf("wrong restored line count: %s", got)
	}
}
