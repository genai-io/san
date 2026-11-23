package automemory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
)

func TestCatalogProjection(t *testing.T) {
	s := storeSnapshot{index: revision{text: "# Prose\r\n\r\nMention an unrelated README.md here.\r\n\r\n- [Topic](./topic.md) — Details\r\n\r\n```md\r\n- [Fake](topic.md)\r\n```\r\n"}}
	want := []catalogEntry{{"topic.md", "Topic", "Details"}}
	if got, complete := s.catalog(); !complete || !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog = %#v, complete = %v", got, complete)
	}
	s.index.text += "\nNonstandard [alias](TOPIC.md).\n"
	if got, complete := s.catalog(); complete || len(got) != 0 {
		t.Fatalf("ambiguous catalog = %#v, complete = %v", got, complete)
	}
}

func TestCatalogBudgets(t *testing.T) {
	var text strings.Builder
	for i := range maxCatalogEntries + 1 {
		fmt.Fprintf(&text, "- [Topic](topic-%d.md)\n", i)
	}
	s := storeSnapshot{index: revision{text: text.String()}}
	if got, complete := s.catalog(); complete || len(got) != maxCatalogEntries {
		t.Fatalf("count-limited catalog has %d entries, complete = %v", len(got), complete)
	}
	text.Reset()
	for i := range maxCatalogEntries {
		fmt.Fprintf(&text, "- [Topic](topic-%d.md) — %s\n", i, strings.Repeat("&", maxCatalogDescription))
	}
	s.index.text = text.String()
	if got, complete := s.catalog(); complete || len(got) == 0 || len(got) >= maxCatalogEntries {
		t.Fatalf("JSON-limited catalog has %d entries, complete = %v", len(got), complete)
	}
}

func TestCatalogDemandReads(t *testing.T) {
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
	const index = "- [One](one.md)\n- [Two](two.md)\n- [Three](three.md)\n- [Four](four.md)\n- [Five](five.md)\n"
	write(system.AutoMemoryIndexName, index)
	for _, name := range []string{"one.md", "two.md", "three.md", "four.md", "five.md", "hidden.md"} {
		write(name, name)
	}
	s, err := snapshotStore(ctx, path)
	if err != nil || len(s.notes) != 0 || s.index.text != index {
		t.Fatalf("snapshot = %#v, err = %v", s, err)
	}
	for _, ids := range [][]string{{"hidden.md"}, {"ONE.md"}, {"./one.md"}, {"one.md", "one.md"}, {"one.md", "two.md", "three.md"}} {
		if got, err := s.readNotes(ctx, path, ids); err == nil || len(got) != 0 {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
	}
	for _, ids := range [][]string{{"one.md", "two.md"}, {"three.md", "four.md"}} {
		got, err := s.readNotes(ctx, path, ids)
		if err != nil || !reflect.DeepEqual(got, []noteEvidence{{ids[0], ids[0]}, {ids[1], ids[1]}}) {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
		if rev := s.note(ids[0]); rev.info == nil || rev.text != ids[0] {
			t.Fatalf("revision = %#v", rev)
		}
		if _, err := s.readNotes(ctx, path, ids[:1]); err == nil {
			t.Fatal("accepted a repeated file")
		}
	}
	if _, err := s.readNotes(ctx, path, []string{"five.md"}); err == nil {
		t.Fatal("accepted a fifth file")
	}

	s, err = snapshotStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	write("five.md", strings.Repeat("&", maxNote))
	if got, err := s.readNotes(ctx, path, []string{"one.md", "five.md"}); err == nil || len(got) != 0 || len(s.notes) != 0 {
		t.Fatalf("oversized JSON batch = %#v, err = %v, revisions = %d", got, err, len(s.notes))
	}
	write("one.md", strings.Repeat("a", maxNote-100))
	write("two.md", strings.Repeat("b", maxNote-100))
	if _, err := s.readNotes(ctx, path, []string{"one.md", "two.md"}); err != nil {
		t.Fatal(err)
	}
	write("three.md", strings.Repeat("c", 512))
	if got, err := s.readNotes(ctx, path, []string{"three.md"}); err == nil || len(got) != 0 || len(s.notes) != 2 {
		t.Fatalf("second batch exceeded the remaining budget: got %d notes, err = %v", len(got), err)
	}
	write(system.AutoMemoryIndexName, index+"\n# Changed\n")
	if got, err := s.readNotes(ctx, path, []string{"four.md"}); !errors.Is(err, errStale) || len(got) != 0 {
		t.Fatalf("stale read = %#v, err = %v", got, err)
	}
}
