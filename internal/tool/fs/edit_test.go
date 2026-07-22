package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func editOnce(filePath, oldText, newText string, cwd string) interface {
	FormatForLLM() string
} {
	result := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"path":  filePath,
		"edits": []any{map[string]any{"oldText": oldText, "newText": newText}},
	}, cwd)
	return result
}

func TestEditTrailingWhitespaceFallback(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "code.go")
	// File lines carry trailing spaces the model's oldText won't have.
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
	// (newText carries the same broken indentation), but the error must
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

	result := editOnce(filePath, "hello", "goodbye", tmpDir)
	if !strings.Contains(result.FormatForLLM(), "has not been read in this session") {
		t.Fatalf("edit without read must be rejected, got: %s", result.FormatForLLM())
	}
}

func TestEditRejectsStaleRead(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "stale.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// External modification after the read: content and size change.
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := editOnce(filePath, "hello", "goodbye", tmpDir)
	if !strings.Contains(result.FormatForLLM(), "changed on disk") {
		t.Fatalf("edit against a stale read must be rejected, got: %s", result.FormatForLLM())
	}

	// Re-reading clears the staleness.
	readForEdit(t, filePath, tmpDir)
	ok := editOnce(filePath, "hello world", "goodbye", tmpDir)
	if strings.Contains(ok.FormatForLLM(), "Error") {
		t.Fatalf("edit after re-read should succeed, got: %s", ok.FormatForLLM())
	}
}

func TestResetReadStampsForgetsReads(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "reset.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// /clear and session switches reset the stamps: the new conversation has
	// no Read results, so the gate must demand a fresh Read.
	ResetReadStamps()
	result := editOnce(filePath, "hello", "goodbye", tmpDir)
	if !strings.Contains(result.FormatForLLM(), "has not been read in this session") {
		t.Fatalf("edit after stamp reset must require a fresh read, got: %s", result.FormatForLLM())
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
	if out := editOnce(filePath, "123", "23", tmpDir).FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("edit after own Write should need no Read, got: %s", out)
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
	// only the tool's own stamp refresh keeps it fresh.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filePath, old, old); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	if out := editOnce(filePath, "one", "1", tmpDir).FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("first edit failed: %s", out)
	}
	if out := editOnce(filePath, "two", "2", tmpDir).FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("second edit after own write should stay fresh, got: %s", out)
	}
}

func TestWriteOverwriteRequiresFreshRead(t *testing.T) {
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
	if out := write().FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("overwrite after read should succeed, got: %s", out)
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
	if out := result.FormatForLLM(); strings.Contains(out, "Error") {
		t.Fatalf("creating a new file must not require a read, got: %s", out)
	}
}
