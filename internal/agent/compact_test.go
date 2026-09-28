package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/filecache"
)

func TestPostCompactRemindersFitFilesToBudget(t *testing.T) {
	dir := t.TempDir()
	files := filecache.New()
	for _, name := range []string{"older.go", "newer.go"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
			t.Fatal(err)
		}
		files.Touch(path)
		time.Sleep(time.Millisecond)
	}

	restored := func(budget int) string {
		return strings.Join(postCompactReminders(dir, files, budget), "\n")
	}
	if got := restored(150); !strings.Contains(got, "newer.go") || strings.Contains(got, "older.go") {
		t.Fatalf("budget 150 should restore only the newest file, got:\n%s", got)
	}
	if got := restored(0); !strings.Contains(got, "newer.go") || !strings.Contains(got, "older.go") {
		t.Fatalf("an unknown budget should restore every recent file, got:\n%s", got)
	}
}
