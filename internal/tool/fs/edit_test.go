package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/tool/toolresult"
)

func editOnce(filePath, oldString, newString, cwd string) toolresult.ToolResult {
	return (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":  filePath,
		"old_string": oldString,
		"new_string": newString,
	}, cwd)
}

func TestEditReplaceAll(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "rename.go")
	content := "count := 1\nprint(count)\nreturn count\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// Without replace_all the ambiguity is an error that names the way out.
	res := editOnce(filePath, "count", "total", tmpDir)
	if res.Success || !strings.Contains(res.Error, "matches 3 locations") || !strings.Contains(res.Error, "replace_all") {
		t.Fatalf("ambiguous edit should count matches and suggest replace_all, got: %+v", res)
	}

	result := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":   filePath,
		"old_string":  "count",
		"new_string":  "total",
		"replace_all": true,
	}, tmpDir)
	if !result.Success || !strings.Contains(result.Output, "3 replacement(s)") {
		t.Fatalf("replace_all should report every occurrence, got: %+v", result)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != "total := 1\nprint(total)\nreturn total\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestEditRejectsIdenticalStrings(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "noop.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	res := editOnce(filePath, "hello", "hello", tmpDir)
	if res.Success || !strings.Contains(res.Error, "must be different") {
		t.Fatalf("a no-op edit must be rejected, got: %+v", res)
	}
}

func TestEditTrailingWhitespaceFallback(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "code.go")
	// File lines carry trailing spaces the model's old_string won't have.
	content := "func main() {  \n\tprintln(\"hi\")\t\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	res := editOnce(filePath, "func main() {\n\tprintln(\"hi\")\n}\n", "func main() {\n\tprintln(\"bye\")\n}\n", tmpDir)
	if !res.Success {
		t.Fatalf("trailing-whitespace fallback should apply, got: %s", res.Error)
	}
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "func main() {\n\tprintln(\"bye\")\n}\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestEditIndentationMismatchEchoesFileLines(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "code.go")
	content := "func main() {\n\tif ok {\n\t\tprintln(\"hi\")\n\t}\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// Model transcribed the leading tabs as spaces — must NOT be applied
	// (new_string carries the same broken indentation), but the error must
	// locate the lines and echo the file's real bytes.
	res := editOnce(filePath, "    if ok {\n        println(\"hi\")\n    }", "    if ok {\n        println(\"bye\")\n    }", tmpDir)
	if res.Success {
		t.Fatal("indentation mismatch must not be applied")
	}
	if !strings.Contains(res.Error, "lines 2-4") {
		t.Fatalf("error should locate the mismatch, got: %s", res.Error)
	}
	if !strings.Contains(res.Error, "\tif ok {") {
		t.Fatalf("error should echo the file's actual tab-indented lines, got: %s", res.Error)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != content {
		t.Fatalf("file must be unchanged, got %q", got)
	}
}

func TestEditRequiresReadFirst(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "unread.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := editOnce(filePath, "hello", "goodbye", tmpDir)
	if res.Success || !strings.Contains(res.Error, "has not been read in this session") {
		t.Fatalf("edit without read must be rejected, got: %+v", res)
	}
}

func TestEditStaleViewSoftApply(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "stale.txt")
	if err := os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// External modification after the read. The edit target still matches
	// exactly and uniquely, so it applies — with a warning, not a block.
	if err := os.WriteFile(filePath, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := editOnce(filePath, "beta", "BETA", tmpDir)
	if !res.Success {
		t.Fatalf("clean match on a stale view should apply, got: %s", res.Error)
	}
	if !strings.Contains(res.Output, "applied cleanly") || !strings.Contains(res.Output, "changed on disk") {
		t.Fatalf("stale apply should carry the warning note, got: %s", res.Output)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestEditStaleViewMismatchNamesStaleness(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "stale2.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	if err := os.WriteFile(filePath, []byte("something else entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := editOnce(filePath, "hello", "goodbye", tmpDir)
	if res.Success || !strings.Contains(res.Error, "changed on disk after it was last read") || !strings.Contains(res.Error, "Read the file again") {
		t.Fatalf("stale mismatch should name the staleness and the recovery, got: %+v", res)
	}

	// Re-reading clears the staleness.
	readForEdit(t, filePath, tmpDir)
	if res := editOnce(filePath, "something else", "anything", tmpDir); !res.Success {
		t.Fatalf("edit after re-read should succeed, got: %s", res.Error)
	}
}

func TestResetFileViewsForgetsObservations(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "reset.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// /clear and session switches reset the views: the new conversation has
	// no Read results, so the gate must demand a fresh Read.
	ResetFileViews()
	res := editOnce(filePath, "hello", "goodbye", tmpDir)
	if res.Success || !strings.Contains(res.Error, "has not been read in this session") {
		t.Fatalf("edit after view reset must require a fresh read, got: %+v", res)
	}
}

func TestEditAfterOwnWriteNeedsNoRead(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "hello.txt")

	// The exact flow from live testing: Write a new file, then Edit it
	// repeatedly — no Read anywhere. The tool's own results are the view.
	written := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"content":   "123",
	}, tmpDir)
	if !written.Success {
		t.Fatalf("write failed: %s", written.Error)
	}
	res := editOnce(filePath, "123", "23", tmpDir)
	if !res.Success {
		t.Fatalf("edit after own Write should need no Read, got: %s", res.Error)
	}
	if !strings.Contains(res.Output, "no need to re-read") {
		t.Fatalf("fresh edit result should suppress the verify-read reflex, got: %s", res.Output)
	}
	if res := editOnce(filePath, "23", "5", tmpDir); !res.Success {
		t.Fatalf("edit after own Edit should need no Read, got: %s", res.Error)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != "5" {
		t.Fatalf("file content = %q", got)
	}
}

func TestEditKeepsOwnWriteFresh(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "chain.txt")
	if err := os.WriteFile(filePath, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure the second edit happens with a different mtime than the read;
	// only the tool's own view refresh keeps it current.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filePath, old, old); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	if res := editOnce(filePath, "one", "1", tmpDir); !res.Success || strings.Contains(res.Output, "applied cleanly") {
		t.Fatalf("first edit should be a plain fresh apply: %+v", res)
	}
	if res := editOnce(filePath, "two", "2", tmpDir); !res.Success || strings.Contains(res.Output, "applied cleanly") {
		t.Fatalf("second edit after own write should stay current, got: %+v", res)
	}
}

func TestWriteOverwriteRequiresCurrentView(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(filePath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	write := func() toolresult.ToolResult {
		return (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
			"file_path": filePath,
			"content":   "replaced\n",
		}, tmpDir)
	}

	if res := write(); res.Success || !strings.Contains(res.Error, "has not been read in this session") {
		t.Fatalf("overwrite without read must be rejected, got: %+v", res)
	}

	readForEdit(t, filePath, tmpDir)
	res := write()
	if !res.Success {
		t.Fatalf("overwrite after read should succeed, got: %s", res.Error)
	}
	if !strings.Contains(res.Output, "use Edit for modifications") {
		t.Fatalf("overwrite result should nudge toward Edit, got: %s", res.Output)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != "replaced\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestWriteNewFileNeedsNoRead(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fresh.txt")

	res := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"content":   "hello\n",
	}, tmpDir)
	if !res.Success {
		t.Fatalf("creating a new file must not require a read, got: %s", res.Error)
	}
	if strings.Contains(res.Output, "use Edit for modifications") {
		t.Fatalf("creating a new file should not carry the overwrite note, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "no need to re-read") {
		t.Fatalf("create result should suppress the verify-read reflex, got: %s", res.Output)
	}
}
