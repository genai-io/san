package automemory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core/system"
)

// Run a synchronous mutation at the first cancellation check after the first
// step has saved its note, without depending on scheduler timing.
type afterStoreStep struct {
	context.Context
	first string
	after func()
}

func (c *afterStoreStep) Err() error {
	if c.after != nil {
		if _, err := os.Stat(c.first); err == nil {
			after := c.after
			c.after = nil
			after()
		}
	}
	return c.Context.Err()
}

func TestStoreBatchRechecksReadOnlyEvidence(t *testing.T) {
	path, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(path, "evidence.md")
	put(t, evidence, "Original evidence.\n")
	put(t, filepath.Join(path, system.AutoMemoryIndexName), "- [Evidence](evidence.md) — Read-only evidence\n")
	s, err := snapshotStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.readNotes(t.Context(), path, []string{"evidence.md"}, map[string]bool{"evidence.md": true}); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(path, "first.md")
	ctx := &afterStoreStep{Context: t.Context(), first: first, after: func() {
		put(t, evidence, "Externally changed evidence.\n")
	}}
	notice, err := apply(ctx, path, s,
		proposal{Action: actionRemember, File: "first.md", Name: "First", Description: "First note", Type: "project", Text: "First body.\n"},
		proposal{Action: actionRemember, File: "second.md", Name: "Second", Description: "Second note", Type: "project", Text: "Second body.\n"},
	)
	if !errors.Is(err, errStale) || notice != "Memory · saved first.md (update incomplete)" {
		t.Fatalf("want partial commit with stale error, got %q, %v", notice, err)
	}
	if get(t, first) != "First body.\n" {
		t.Fatal("first note was not committed")
	}
	if _, err := os.Stat(filepath.Join(path, "second.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("second note should not exist: %v", err)
	}
}

func TestStoreRememberReportsUnindexedNote(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Occupy the absent index's destination so its exclusive install fails,
	// without affecting the note's install or relying on file permissions.
	if err := root.Mkdir(system.AutoMemoryIndexName, 0700); err != nil {
		t.Fatal(err)
	}
	step := plannedChange{
		proposal: proposal{Action: actionRemember, File: "first.md", Text: "First body.\n"},
		index:    "- [First](first.md) — First note\n",
	}
	var index revision
	partial, err := step.commit(root, &index)
	if !errors.Is(err, fs.ErrExist) || partial != "saved first.md without indexing it" {
		t.Fatalf("want unindexed note and failed index install, got %q, %v", partial, err)
	}
	if got, err := root.ReadFile("first.md"); err != nil || string(got) != "First body.\n" {
		t.Fatalf("committed note = %q, %v", got, err)
	}
}
