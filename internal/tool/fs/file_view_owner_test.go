package fs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileViewsOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared")
	if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	a, b := WithFileViews(context.Background(), NewFileViews()), WithFileViews(context.Background(), NewFileViews())
	read := func(ctx context.Context) {
		t.Helper()
		if r := (&ReadTool{}).Execute(ctx, map[string]any{"file_path": path}, ""); !r.Success {
			t.Fatal(r)
		}
	}
	read(a)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := requireCurrentView(path, a); err != nil {
				t.Error(err)
			}
			if err := requireCurrentView(path, b); err == nil {
				t.Error("other owner's read authorized overwrite")
			}
		})
	}
	wg.Wait()
	read(b)
	write := func(ctx context.Context, content string) bool {
		return (&WriteTool{}).Execute(ctx, map[string]any{"file_path": path, "content": content}, "").Success
	}
	if !write(a, "a longer replacement") {
		t.Fatal("owner write failed")
	}
	if write(b, "lost update") {
		t.Fatal("other owner's write refreshed stale view")
	}
	ResetFileViews()
	if !write(a, "still current across prompts and default reset") {
		t.Fatal("owner observation lost")
	}
	if Viewed(path) {
		t.Fatal("scoped observations leaked to native default")
	}
}
