//go:build darwin || linux

package fs

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestFailedFileWriteKeepsOriginal(t *testing.T) {
	// Isolate the process-wide file-size limit from other tests.
	if os.Getenv("SAN_TEST_WRITE_FAILURE") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFailedFileWriteKeepsOriginal$")
		cmd.Env = append(os.Environ(), "SAN_TEST_WRITE_FAILURE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write failure test: %v\n%s", err, out)
		}
		return
	}
	var originalLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &originalLimit); err != nil {
		t.Fatal(err)
	}
	limit := originalLimit
	limit.Cur = 2
	signal.Ignore(syscall.SIGXFSZ)
	defer signal.Reset(syscall.SIGXFSZ)
	for _, name := range []string{"Write", "Edit"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "valuable.txt")
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		readForEdit(t, path, dir)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
			t.Fatal(err)
		}
		var result toolresult.ToolResult
		if name == "Write" {
			result = (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
				"file_path": path, "content": "replacement",
			}, dir)
		} else {
			result = editOnce(path, "original", "replacement", dir)
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &originalLimit); err != nil {
			t.Fatal(err)
		}
		if result.Success {
			t.Fatalf("%s succeeded despite file-size limit", name)
		}
		if body, err := os.ReadFile(path); err != nil || string(body) != "original" {
			t.Fatalf("%s damaged original: %q, err = %v", name, body, err)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
			t.Fatalf("%s left a temporary file: %v, err = %v", name, entries, err)
		}
	}
}
