package fs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Models send modes as decimal JSON numbers (755 is octal 1363). Write takes
// no mode, so a new file stays readable by its owner.
func TestWriteIgnoresModeArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "new.sh")
	r := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{"file_path": path, "content": "x", "mode": 755}, dir)
	if !r.Success {
		t.Fatal(r.Error)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky != 0 || info.Mode().Perm()&0o600 != 0o600 || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("mode = %v, want 0644 less umask", info.Mode())
	}
}
