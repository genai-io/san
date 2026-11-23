package automemory

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

func TestUpdateIndexRawMarkdown(t *testing.T) {
	index := "# Prose\r\n\r\nMention an unrelated README.md here.\r\n\r\n- [Topic](./topic.md) — Details\r\n\r\n```md\r\n- [Fake](topic.md)\r\n- [Literal](literal.md)\r\n```\r\n<!--\r\n- [Hidden](comment.md)\r\n-->"
	limits := setting.MemoryPreviewLimits{}
	snapshot := newIndex(index, limits)
	preview, _ := snapshot.Preview()
	ids, problem := snapshot.updateNoteIDs()
	if problem != "" || preview != index {
		t.Fatalf("update index = %q, problem = %q", preview, problem)
	}
	if !reflect.DeepEqual(ids, map[string]bool{"topic.md": true}) {
		t.Fatalf("note IDs = %v", ids)
	}
	ids, problem = newIndex(index+"\nNonstandard [alias](TOPIC.md).\n", limits).updateNoteIDs()
	if problem == "" {
		t.Fatal("ambiguous index was not detected")
	}
	if _, valid := ids["topic.md"]; valid {
		t.Fatal("ambiguous ID was allowed")
	}
}

func TestUpdateIndexAmbiguityBeyondPreview(t *testing.T) {
	prefix := "- [Topic](topic.md)\n" + strings.Repeat("# Heading\n", 199)
	index := newIndex(prefix+"- [Hidden](hidden.md)\n- [Alias](TOPIC.md)\n", setting.MemoryPreviewLimits{})
	if got, more := index.Preview(); !more || got != prefix {
		t.Fatal("preview did not stop after 200 complete lines")
	}
	ids, problem := index.updateNoteIDs()
	if !reflect.DeepEqual(ids, map[string]bool{"hidden.md": false}) || problem == "" {
		t.Fatalf("note IDs = %v, problem = %q", ids, problem)
	}
}

func TestUpdateIndexDemandReads(t *testing.T) {
	ctx := t.Context()
	path, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	index := "- [One](one.md)\n- [Two](two.md)\n- [Three](three.md)\n- [Four](four.md)\n- [Five](five.md)\n- [Device](CON.md)\n```\n- [Literal](literal.md)\n```\n"
	index += "\n" + strings.Repeat("x", 25_000) + "\n- [Omitted](omitted.md)\n"
	write(system.AutoMemoryIndexName, index)
	for _, name := range []string{"one.md", "two.md", "three.md", "four.md", "five.md", "hidden.md", "omitted.md", "literal.md"} {
		write(name, name)
	}
	s, err := snapshotStore(ctx, path)
	if err != nil || len(s.notes) != 0 || s.index.text != index {
		t.Fatalf("snapshot = %#v, err = %v", s, err)
	}
	visible, _ := newIndex(s.index.text, setting.MemoryPreviewLimits{}).updateNoteIDs()
	for _, ids := range [][]string{{"hidden.md"}, {"omitted.md"}, {"literal.md"}, {"CON.md"}, {"MEMORY.md"}, {"ONE.md"}, {"./one.md"}, {"one.md", "one.md"}, {"one.md", "two.md", "three.md"}} {
		if got, err := s.readNotes(ctx, path, ids, visible); err == nil || len(got) != 0 {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
	}
	for _, ids := range [][]string{{"one.md", "two.md"}, {"three.md"}} {
		want := make([]noteData, 0, len(ids))
		for _, id := range ids {
			want = append(want, noteData{File: id, Text: id})
		}
		got, err := s.readNotes(ctx, path, ids, visible)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
		if rev := s.note(ids[0]); rev.info == nil || rev.text != ids[0] {
			t.Fatalf("revision = %#v", rev)
		}
		if _, err := s.readNotes(ctx, path, ids[:1], visible); err == nil {
			t.Fatal("accepted a repeated file")
		}
	}
	if _, err := s.readNotes(ctx, path, []string{"four.md"}, visible); err == nil {
		t.Fatal("accepted a third read round")
	}

	s, err = snapshotStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	write("one.md", strings.Repeat("&", maxNote))
	write("two.md", strings.Repeat("b", maxNote))
	if _, err := s.readNotes(ctx, path, []string{"one.md", "two.md"}, visible); err != nil {
		t.Fatal(err)
	}
	write("three.md", strings.Repeat("c", 512))
	if got, err := s.readNotes(ctx, path, []string{"three.md"}, visible); err == nil || len(got) != 0 || len(s.notes) != 2 {
		t.Fatalf("second batch exceeded the remaining budget: got %d notes, err = %v", len(got), err)
	}
	write(system.AutoMemoryIndexName, index+"\n# Changed\n")
	if got, err := s.readNotes(ctx, path, []string{"four.md"}, visible); !errors.Is(err, errStale) || len(got) != 0 {
		t.Fatalf("stale read = %#v, err = %v", got, err)
	}
}
