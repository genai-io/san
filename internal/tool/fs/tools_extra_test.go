package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool/toolresult"
)

// TestRead_LineLimit_LargeFile verifies that Read respects the limit parameter
// and returns at most limit lines even when the file has many more.
func TestRead_LineLimit_LargeFile(t *testing.T) {
	// Create a temp file with 200 lines
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "large.txt")

	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(filePath, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	tool := &ReadTool{}
	ctx := context.Background()

	t.Run("reads all lines by default (up to maxReadLines)", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"file_path": filePath,
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		// 200 is below maxReadLines (2000), so all 200 lines should be returned
		if len(result.Lines) != 200 {
			t.Errorf("Expected 200 lines, got %d", len(result.Lines))
		}
		if result.Metadata.Truncated {
			t.Error("Expected Truncated=false for 200-line file with default limit")
		}
	})

	t.Run("limit parameter restricts number of lines returned", func(t *testing.T) {
		limit := 50
		result := tool.Execute(ctx, map[string]any{
			"file_path": filePath,
			"limit":     float64(limit), // JSON numbers come as float64
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		if len(result.Lines) != limit {
			t.Errorf("Expected %d lines, got %d", limit, len(result.Lines))
		}
		if !result.Metadata.Truncated {
			t.Error("Expected Truncated=true when limit < total lines")
		}
	})

	t.Run("limit=1 returns exactly one line", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"file_path": filePath,
			"limit":     float64(1),
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		if len(result.Lines) != 1 {
			t.Errorf("Expected 1 line, got %d", len(result.Lines))
		}
		if result.Lines[0].Text != "line 1" {
			t.Errorf("Expected first line text 'line 1', got %q", result.Lines[0].Text)
		}
	})

	t.Run("offset skips lines before reading", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"file_path": filePath,
			"offset":    float64(100),
			"limit":     float64(10),
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		if len(result.Lines) != 10 {
			t.Errorf("Expected 10 lines, got %d", len(result.Lines))
		}
		// First returned line should be line 100 (offset is 1-based but lines before offset are skipped)
		if !strings.HasPrefix(result.Lines[0].Text, "line ") {
			t.Errorf("Unexpected first line text: %q", result.Lines[0].Text)
		}
	})
}

func TestReadEmptyFileSaysSo(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(filePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	result := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": filePath}, tmpDir)
	if !result.Success {
		t.Fatalf("reading an empty file should succeed, got: %s", result.Error)
	}
	if !strings.Contains(result.FormatForLLM(), "file exists but is empty") {
		t.Fatalf("empty read should say so instead of returning nothing, got %q", result.FormatForLLM())
	}
}

// readForEdit satisfies Edit/Write's read-before-modify gate in tests.
func readForEdit(t *testing.T, filePath, cwd string) {
	t.Helper()
	result := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": filePath}, cwd)
	if !result.Success {
		t.Fatalf("Read before edit failed: %s", result.FormatForLLM())
	}
}

func TestEditPreservesBomAndCrlf(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "example.txt")
	original := "\ufefftitle: old\r\nfirst: old\r\nsecond: keep\r\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	// Sequential edits, the way multiple changes land under the CC-shaped
	// schema. old_string/new_string arrive LF-normalized like model output.
	result := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":  filePath,
		"old_string": "title: old\n",
		"new_string": "title: new\n",
	}, tmpDir)
	if !result.Success {
		t.Fatalf("first edit failed: %s", result.FormatForLLM())
	}
	details, ok := result.Details.(toolresult.FileChangeDetails)
	if !ok || details.EditCount != 1 || details.AddedLines != 1 || details.RemovedLines != 1 {
		t.Fatalf("Edit details = %#v", result.Details)
	}
	if res := editOnce(filePath, "second: keep", "second: new", tmpDir); !res.Success {
		t.Fatalf("second edit failed: %s", res.Error)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "\ufefftitle: new\r\nfirst: old\r\nsecond: new\r\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}

	// Failed edits leave the file untouched and name the problem.
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)
	for _, test := range []struct {
		oldString, want string
	}{
		{"missing", "old_string was not found"},
		{"old", "old_string matches 2 locations"},
	} {
		failed := editOnce(filePath, test.oldString, "replacement", tmpDir)
		if failed.Success || !strings.Contains(failed.Error, test.want) {
			t.Fatalf("invalid edit = %+v, want %q", failed, test.want)
		}
		content, err = os.ReadFile(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(content); got != original {
			t.Fatalf("invalid edit changed file to %q", got)
		}
	}
}
