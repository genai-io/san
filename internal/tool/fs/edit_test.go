package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func editOnce(filePath, oldString, newString, cwd string) interface {
	FormatForLLM() string
} {
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
	out := editOnce(filePath, "count", "total", tmpDir).FormatForLLM()
	if !strings.Contains(out, "matches 3 locations") || !strings.Contains(out, "replace_all") {
		t.Fatalf("ambiguous edit should count matches and suggest replace_all, got: %s", out)
	}

	result := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":   filePath,
		"old_string":  "count",
		"new_string":  "total",
		"replace_all": true,
	}, tmpDir)
	if out := result.FormatForLLM(); !strings.Contains(out, "3 replacement(s)") {
		t.Fatalf("replace_all should report every occurrence, got: %s", out)
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

	out := editOnce(filePath, "hello", "hello", tmpDir).FormatForLLM()
	if !strings.Contains(out, "must be different") {
		t.Fatalf("a no-op edit must be rejected, got: %s", out)
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

	result := editOnce(filePath, "func main() {\n\tprintln(\"hi\")\n}\n", "func main() {\n\tprintln(\"bye\")\n}\n", tmpDir)
	out := result.FormatForLLM()
	if strings.Contains(out, "Error") {
		t.Fatalf("trailing-whitespace fallback should apply, got: %s", out)
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
	result := editOnce(filePath, "    if ok {\n        println(\"hi\")\n    }", "    if ok {\n        println(\"bye\")\n    }", tmpDir)
	out := result.FormatForLLM()
	if !strings.Contains(out, "lines 2-4") {
		t.Fatalf("error should locate the mismatch, got: %s", out)
	}
	if !strings.Contains(out, "\tif ok {") {
		t.Fatalf("error should echo the file's actual tab-indented lines, got: %s", out)
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

	out := editOnce(filePath, "hello", "goodbye", tmpDir).FormatForLLM()
	if !strings.Contains(out, "has not been read in this session") {
		t.Fatalf("edit without read must be rejected, got: %s", out)
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
	out := editOnce(filePath, "beta", "BETA", tmpDir).FormatForLLM()
	if strings.Contains(out, "Error") {
		t.Fatalf("clean match on a stale view should apply, got: %s", out)
	}
	if !strings.Contains(out, "applied cleanly") || !strings.Contains(out, "changed on disk") {
		t.Fatalf("stale apply should carry the warning note, got: %s", out)
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
	out := editOnce(filePath, "hello", "goodbye", tmpDir).FormatForLLM()
	if !strings.Contains(out, "changed on disk after it was last read") || !strings.Contains(out, "Read the file again") {
		t.Fatalf("stale mismatch should name the staleness and the recovery, got: %s", out)
	}

	// Re-reading clears the staleness.
	readForEdit(t, filePath, tmpDir)
	if out := editOnce(filePath, "something else", "anything", tmpDir).FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("edit after re-read should succeed, got: %s", out)
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
	out := editOnce(filePath, "hello", "goodbye", tmpDir).FormatForLLM()
	if !strings.Contains(out, "has not been read in this session") {
		t.Fatalf("edit after view reset must require a fresh read, got: %s", out)
	}
}

func TestEditAfterOwnWriteNeedsNoRead(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "hello.txt")

	// The exact flow from live testing: Write a new file, then Edit it
	// repeatedly — no Read anywhere. The tool's own results are the view.
	result := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"content":   "123",
	}, tmpDir)
	if out := result.FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("write failed: %s", out)
	}
	out := editOnce(filePath, "123", "23", tmpDir).FormatForLLM()
	if strings.Contains(out, "Error") {
		t.Fatalf("edit after own Write should need no Read, got: %s", out)
	}
	if !strings.Contains(out, "no need to re-read") {
		t.Fatalf("fresh edit result should suppress the verify-read reflex, got: %s", out)
	}
	if out := editOnce(filePath, "23", "5", tmpDir).FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("edit after own Edit should need no Read, got: %s", out)
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

	if out := editOnce(filePath, "one", "1", tmpDir).FormatForLLM(); strings.Contains(out, "Error") || strings.Contains(out, "applied cleanly") {
		t.Fatalf("first edit should be a plain fresh apply: %s", out)
	}
	if out := editOnce(filePath, "two", "2", tmpDir).FormatForLLM(); strings.Contains(out, "Error") || strings.Contains(out, "applied cleanly") {
		t.Fatalf("second edit after own write should stay current, got: %s", out)
	}
}

func TestWriteOverwriteRequiresCurrentView(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(filePath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	write := func() interface{ FormatForLLM() string } {
		return (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
			"file_path": filePath,
			"content":   "replaced\n",
		}, tmpDir)
	}

	if out := write().FormatForLLM(); !strings.Contains(out, "has not been read in this session") {
		t.Fatalf("overwrite without read must be rejected, got: %s", out)
	}

	readForEdit(t, filePath, tmpDir)
	out := write().FormatForLLM()
	if strings.Contains(out, "Error") {
		t.Fatalf("overwrite after read should succeed, got: %s", out)
	}
	if !strings.Contains(out, "use Edit for modifications") {
		t.Fatalf("overwrite result should nudge toward Edit, got: %s", out)
	}
	got, _ := os.ReadFile(filePath)
	if string(got) != "replaced\n" {
		t.Fatalf("file content = %q", got)
	}
}

func TestWriteNewFileNeedsNoRead(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fresh.txt")

	result := (&WriteTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"content":   "hello\n",
	}, tmpDir)
	out := result.FormatForLLM()
	if strings.Contains(out, "Error") {
		t.Fatalf("creating a new file must not require a read, got: %s", out)
	}
	if strings.Contains(out, "use Edit for modifications") {
		t.Fatalf("creating a new file should not carry the overwrite note, got: %s", out)
	}
	if !strings.Contains(out, "no need to re-read") {
		t.Fatalf("create result should suppress the verify-read reflex, got: %s", out)
	}
}
