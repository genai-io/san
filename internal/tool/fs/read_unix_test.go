//go:build unix

package fs

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	result := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": fifo}, dir)
	if result.Success || !strings.Contains(result.Error, "not a regular file") {
		t.Fatalf("Read of a FIFO should fail fast, got %+v", result)
	}
}
