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

// readForEdit satisfies Edit/Write's read-before-modify gate in tests.
func readForEdit(t *testing.T, filePath, cwd string) {
	t.Helper()
	result := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": filePath}, cwd)
	if !result.Success {
		t.Fatalf("Read before edit failed: %s", result.FormatForLLM())
	}
}

func TestEditBatchReplacements(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "example.txt")
	original := "\ufefftitle: old\r\nfirst: old\r\nsecond: keep\r\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)

	params := map[string]any{
		"path": filePath,
		"edits": []any{
			map[string]any{"oldText": "title: old\n", "newText": "title: new\n"},
			map[string]any{"oldText": "second: keep", "newText": "second: new"},
		},
	}
	result := (&EditTool{}).ExecuteApproved(context.Background(), params, tmpDir)
	if !result.Success {
		t.Fatalf("batch Edit failed: %s", result.FormatForLLM())
	}
	if !strings.Contains(result.Output, "2 replacements, +2 -2") {
		t.Fatalf("batch Edit output = %q", result.Output)
	}
	details, ok := result.Details.(toolresult.FileChangeDetails)
	if !ok || details.EditCount != 2 || details.AddedLines != 2 || details.RemovedLines != 2 {
		t.Fatalf("Edit details = %#v", result.Details)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "\ufefftitle: new\r\nfirst: old\r\nsecond: new\r\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}

	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	readForEdit(t, filePath, tmpDir)
	for _, test := range []struct {
		edits []any
		want  string
	}{
		{[]any{map[string]any{"oldText": "title: old", "newText": "title: new"}, map[string]any{"oldText": "missing", "newText": "replacement"}}, "edits[1]: oldText was not found"},
		{[]any{map[string]any{"oldText": "old", "newText": "new"}}, "edits[0]: oldText matches 2 locations"},
		{[]any{map[string]any{"oldText": "title: old", "newText": "title: new"}, map[string]any{"oldText": "old\nfirst", "newText": "new\nfirst"}}, "edits[0] overlaps edits[1]"},
	} {
		failed := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{"path": filePath, "edits": test.edits}, tmpDir)
		if failed.Success || !strings.Contains(failed.FormatForLLM(), test.want) {
			t.Fatalf("invalid batch Edit = %+v, want %q", failed, test.want)
		}
		content, err = os.ReadFile(filePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(content); got != original {
			t.Fatalf("invalid batch Edit changed file to %q", got)
		}
	}
}
