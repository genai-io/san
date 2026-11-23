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

func TestUpdateIndexRawMarkdown(t *testing.T) {
	index := "# Prose\r\n\r\nMention an unrelated README.md here.\r\n\r\n- [Topic](./topic.md) — Details\r\n\r\n```md\r\n- [Fake](topic.md)\r\n- [Literal](literal.md)\r\n```\r\n<!--\r\n- [Hidden](comment.md)\r\n-->"
	s := storeSnapshot{index: revision{text: index}}
	if got, complete := s.updateIndex(); !complete || got != index {
		t.Fatalf("update index = %q, complete = %v", got, complete)
	}
	ids, _ := s.updateNoteIDs(s.updateIndexEnd())
	if !reflect.DeepEqual(ids, map[string]bool{"topic.md": true}) {
		t.Fatalf("note IDs = %v", ids)
	}
	s.index.text += "\nNonstandard [alias](TOPIC.md).\n"
	if got, complete := s.updateIndex(); complete || got != s.index.text {
		t.Fatalf("ambiguous update index = %q, complete = %v", got, complete)
	}
	ids, _ = s.updateNoteIDs(s.updateIndexEnd())
	if ids["topic.md"] {
		t.Fatal("ambiguous ID was allowed")
	}
}

func TestUpdateIndexByteLimit(t *testing.T) {
	var text strings.Builder
	for i := range 40 {
		fmt.Fprintf(&text, "- [%s](topic-%d.md) — %s\n", strings.Repeat("T", 90), i, strings.Repeat("&", 70))
	}
	text.WriteString(strings.Repeat("x", maxUpdateIndex-text.Len()-1))
	text.WriteByte('\n')
	prefix := text.String()
	s := storeSnapshot{index: revision{text: prefix}}
	if got, complete := s.updateIndex(); !complete || got != prefix {
		t.Fatalf("at limit: length = %d, complete = %v", len(got), complete)
	}
	s.index.text += "- [Hidden](hidden.md)\n- [Alias](TOPIC-0.md)\n"
	if got, complete := s.updateIndex(); complete || got != prefix {
		t.Fatalf("over limit: length = %d, complete = %v", len(got), complete)
	}
	ids, _ := s.updateNoteIDs(s.updateIndexEnd())
	if ids["hidden.md"] || ids["topic-0.md"] || !ids["topic-39.md"] {
		t.Fatalf("note IDs = %v", ids)
	}
	s.index.text = prefix[:len(prefix)-2] + "\n界"
	if got, complete := s.updateIndex(); complete || got != prefix[:len(prefix)-2]+"\n" {
		t.Fatalf("limit inside multibyte character: length = %d, complete = %v", len(got), complete)
	}
	s.index.text = strings.Repeat("界", maxUpdateIndex)
	if got, complete := s.updateIndex(); complete || got != "" {
		t.Fatalf("no complete line: length = %d, complete = %v", len(got), complete)
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
	index += "\n" + strings.Repeat("x", maxUpdateIndex) + "\n- [Omitted](omitted.md)\n"
	write(system.AutoMemoryIndexName, index)
	for _, name := range []string{"one.md", "two.md", "three.md", "four.md", "five.md", "hidden.md", "omitted.md", "literal.md"} {
		write(name, name)
	}
	s, err := snapshotStore(ctx, path)
	if err != nil || len(s.notes) != 0 || s.index.text != index {
		t.Fatalf("snapshot = %#v, err = %v", s, err)
	}
	for _, ids := range [][]string{{"hidden.md"}, {"omitted.md"}, {"literal.md"}, {"CON.md"}, {"MEMORY.md"}, {"ONE.md"}, {"./one.md"}, {"one.md", "one.md"}, {"one.md", "two.md", "three.md"}} {
		if got, err := s.readNotes(ctx, path, ids); err == nil || len(got) != 0 {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
	}
	for _, ids := range [][]string{{"one.md", "two.md"}, {"three.md"}} {
		want := make([]noteData, 0, len(ids))
		for _, id := range ids {
			want = append(want, noteData{File: id, Text: id})
		}
		got, err := s.readNotes(ctx, path, ids)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("read %v = %#v, err = %v", ids, got, err)
		}
		if rev := s.note(ids[0]); rev.info == nil || rev.text != ids[0] {
			t.Fatalf("revision = %#v", rev)
		}
		if _, err := s.readNotes(ctx, path, ids[:1]); err == nil {
			t.Fatal("accepted a repeated file")
		}
	}
	if _, err := s.readNotes(ctx, path, []string{"four.md"}); err == nil {
		t.Fatal("accepted a third read round")
	}

	s, err = snapshotStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	write("one.md", strings.Repeat("&", maxNote))
	write("two.md", strings.Repeat("b", maxNote))
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
