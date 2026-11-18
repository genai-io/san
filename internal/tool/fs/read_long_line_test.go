package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line longer than any read buffer is cut, not an error for the whole file.
func TestReadCutsLineLongerThanBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.min.js")
	long := strings.Repeat("é", 2<<20)
	if err := os.WriteFile(path, []byte("first\n"+long+"\nlast"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": path}, "")
	if !r.Success {
		t.Fatalf("Read failed: %s", r.Error)
	}
	if len(r.Lines) != 3 || r.Lines[0].Text != "first" || r.Lines[2].Text != "last" {
		t.Fatalf("lines = %d, want first/long/last", len(r.Lines))
	}
	want := strings.Repeat("é", maxLineLen) + lineTruncationMarker
	if r.Lines[1].Text != want {
		t.Errorf("long line not cut to %d runes plus marker (got %d bytes)", maxLineLen, len(r.Lines[1].Text))
	}
}

func TestReadLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	exact := strings.Repeat("x", maxLineLen)
	if err := os.WriteFile(path, []byte("a\r\n\n"+exact+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": path}, "")
	got := make([]string, 0, len(r.Lines))
	for _, l := range r.Lines {
		got = append(got, l.Text)
	}
	if strings.Join(got, "|") != "a||"+exact {
		t.Errorf("lines = %q", got)
	}
}

func TestReadKeepsFinalLineAtBufferBoundary(t *testing.T) {
	for _, length := range []int{4096, 8192} {
		path := filepath.Join(t.TempDir(), "no-newline.txt")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", length)), 0o644); err != nil {
			t.Fatal(err)
		}
		got := (&ReadTool{}).Execute(context.Background(), map[string]any{"file_path": path}, "")
		want := strings.Repeat("x", maxLineLen) + lineTruncationMarker
		if !got.Success || len(got.Lines) != 1 || got.Lines[0].Text != want {
			t.Errorf("last line of %d bytes: got %d lines, error %q", length, len(got.Lines), got.Error)
		}
	}
}
