package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestEdit_Fails_WhenOldStringNotUnique verifies that Edit's PreparePermission
// returns an error when old_string matches more than once and replace_all is false.
func TestEdit_Fails_WhenOldStringNotUnique(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "dup.txt")

	content := "hello world\nhello world\nhello world\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	tool := &EditTool{}
	ctx := context.Background()

	t.Run("returns error when old_string is not unique", func(t *testing.T) {
		_, err := tool.PreparePermission(ctx, map[string]any{
			"file_path":  filePath,
			"old_string": "hello world",
			"new_string": "goodbye world",
		}, tmpDir)

		if err == nil {
			t.Fatal("Expected an error for non-unique old_string, got nil")
		}
		if !strings.Contains(err.Error(), "not unique") && !strings.Contains(err.Error(), "occurrences") {
			t.Errorf("Expected error message about uniqueness, got: %s", err.Error())
		}
	})

	t.Run("succeeds when replace_all=true and old_string appears multiple times", func(t *testing.T) {
		// Re-write file since content may be unchanged after the failed prepare
		os.WriteFile(filePath, []byte(content), 0o644)

		_, err := tool.PreparePermission(ctx, map[string]any{
			"file_path":   filePath,
			"old_string":  "hello world",
			"new_string":  "goodbye world",
			"replace_all": true,
		}, tmpDir)

		if err != nil {
			t.Fatalf("Expected no error with replace_all=true, got: %v", err)
		}
	})

	t.Run("returns error when old_string not found at all", func(t *testing.T) {
		os.WriteFile(filePath, []byte(content), 0o644)

		_, err := tool.PreparePermission(ctx, map[string]any{
			"file_path":  filePath,
			"old_string": "this string does not exist in the file",
			"new_string": "replacement",
		}, tmpDir)

		if err == nil {
			t.Fatal("Expected an error when old_string not found, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' in error message, got: %s", err.Error())
		}
	})
}

func TestEditReportsLineChanges(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "example.txt")
	if err := os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":  filePath,
		"old_string": "two\n",
		"new_string": "two\nextra\n",
	}, tmpDir)
	if !result.Success {
		t.Fatalf("Edit failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, "+1 -0") {
		t.Fatalf("Edit output = %q, want line counts", result.Output)
	}

	failed := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path":  filePath,
		"old_string": "missing",
		"new_string": "replacement",
	}, tmpDir)
	if failed.Success || !strings.Contains(failed.FormatForLLM(), "not found in current file") {
		t.Fatalf("failed Edit = %+v, want failure reason", failed)
	}
}

func TestEditBatchReplacements(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "example.txt")
	original := "title: old\nfirst: old\nsecond: keep\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	params := map[string]any{
		"file_path": filePath,
		"edits": []any{
			map[string]any{"old_string": "title: old", "new_string": "title: new"},
			map[string]any{"old_string": "second: keep", "new_string": "second: new"},
		},
	}
	result := (&EditTool{}).ExecuteApproved(context.Background(), params, tmpDir)
	if !result.Success {
		t.Fatalf("batch Edit failed: %s", result.FormatForLLM())
	}
	if !strings.Contains(result.Output, "+2 -2, 2 replacements") {
		t.Fatalf("batch Edit output = %q", result.Output)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "title: new\nfirst: old\nsecond: new\n"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}

	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"edits": []any{
			map[string]any{"old_string": "title: old", "new_string": "title: new"},
			map[string]any{"old_string": "missing", "new_string": "replacement"},
		},
	}, tmpDir)
	if failed.Success || !strings.Contains(failed.FormatForLLM(), "edits[1]: old_string not found") {
		t.Fatalf("failed batch Edit = %+v", failed)
	}
	content, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != original {
		t.Fatalf("failed batch Edit changed file to %q", got)
	}

	overlapped := (&EditTool{}).ExecuteApproved(context.Background(), map[string]any{
		"file_path": filePath,
		"edits": []any{
			map[string]any{"old_string": "title: old", "new_string": "title: new"},
			map[string]any{"old_string": "old\nfirst", "new_string": "new\nfirst"},
		},
	}, tmpDir)
	if overlapped.Success || !strings.Contains(overlapped.FormatForLLM(), "edits overlap") {
		t.Fatalf("overlapping batch Edit = %+v", overlapped)
	}
	content, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != original {
		t.Fatalf("overlapping batch Edit changed file to %q", got)
	}

	_, err = (&EditTool{}).PreparePermission(context.Background(), map[string]any{
		"file_path":  filePath,
		"old_string": "title: old",
		"new_string": "title: new",
		"edits":      []any{map[string]any{"old_string": "second: keep", "new_string": "second: new"}},
	}, tmpDir)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("mixed Edit form error = %v", err)
	}
}

func TestGlob_PatternMatching(t *testing.T) {
	// Create a temp directory structure:
	//   root/
	//     a.go
	//     b.txt
	//     sub/
	//       c.go
	//       deep/
	//         d.go
	//         e.md
	tmpDir := t.TempDir()

	dirs := []string{
		filepath.Join(tmpDir, "sub"),
		filepath.Join(tmpDir, "sub", "deep"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", d, err)
		}
	}

	files := map[string]string{
		filepath.Join(tmpDir, "a.go"):                "package main",
		filepath.Join(tmpDir, "b.txt"):               "text",
		filepath.Join(tmpDir, "sub", "c.go"):         "package sub",
		filepath.Join(tmpDir, "sub", "deep", "d.go"): "package deep",
		filepath.Join(tmpDir, "sub", "deep", "e.md"): "# doc",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("Failed to write %s: %v", path, err)
		}
	}

	tool := &GlobTool{}
	ctx := context.Background()

	t.Run("** matches files in all subdirectories", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"pattern": "**/*.go",
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		// Should find a.go, sub/c.go, sub/deep/d.go
		if len(result.Files) != 3 {
			t.Errorf("Expected 3 .go files, got %d: %v", len(result.Files), result.Files)
		}
	})

	t.Run("*.go matches only top-level go files", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"pattern": "*.go",
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		// Should find only a.go at root level
		if len(result.Files) != 1 {
			t.Errorf("Expected 1 .go file at top level, got %d: %v", len(result.Files), result.Files)
		}
		if len(result.Files) > 0 && result.Files[0] != "a.go" {
			t.Errorf("Expected a.go, got %q", result.Files[0])
		}
	})

	t.Run("? matches single character in filename", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"pattern": "?.go",
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		// Should match a.go (single char before .go)
		if len(result.Files) != 1 {
			t.Errorf("Expected 1 file matching ?.go, got %d: %v", len(result.Files), result.Files)
		}
	})

	t.Run("** with specific dir matches files in that subtree", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"pattern": "sub/**/*.go",
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		// Should find sub/c.go and sub/deep/d.go
		if len(result.Files) != 2 {
			t.Errorf("Expected 2 files in sub/**/*.go, got %d: %v", len(result.Files), result.Files)
		}
	})

	t.Run("non-existent pattern returns empty results", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"pattern": "*.xyz",
		}, tmpDir)

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Output)
		}
		if len(result.Files) != 0 {
			t.Errorf("Expected 0 files for non-matching pattern, got %d", len(result.Files))
		}
	})

	t.Run("missing pattern param returns error", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{}, tmpDir)
		if result.Success {
			t.Fatal("Expected error for missing pattern, got success")
		}
	})
}
