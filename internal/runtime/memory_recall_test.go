package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRecallSelectNotes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, desc string) string {
		return writeRecallNote(t, dir, name, "---\nname: x\ndescription: "+desc+"\n---\nbody of "+name)
	}
	tabs := write("tabs.md", "tabs over spaces")
	freeze := write("freeze.md", "release freeze")
	if err := os.WriteFile(filepath.Join(dir, "orphan.md"), []byte("---\ndescription: not linked\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := system.MemoryStore(dir)

	driver := aitest.New(aitest.Says(`{"selected_memories":["project/tabs.md","freeze.md","tabs.md","nope.md","MEMORY.md","orphan.md","freeze.md"]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	got, scope, err := r.selectNotes(t.Context(), store, "can I merge?", nil)
	defer scope.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != freeze || got[1].Path != tabs {
		t.Fatalf("selectNotes = %+v, want freeze.md and tabs.md", got)
	}
	if index := driver.Last().Messages[0].Text(); strings.Contains(index, "orphan.md") {
		t.Fatalf("unlinked note offered to Recall: %q", index)
	}
}

func TestRecallKeepsLargeIndexStable(t *testing.T) {
	store := system.MemoryStore(t.TempDir())
	for i := range 60 {
		writeRecallNote(t, string(store), fmt.Sprintf("note-%02d.md", i), "---\ndescription: cooking recipes\n---\n")
	}
	path := writeRecallNote(t, string(store), "freeze.md", "Project policy")
	const query = "Check `freeze.md`, please."
	driver := aitest.New(aitest.Says(`{"selected_memories":["freeze.md"]}`), aitest.Says(`{"selected_memories":["note-00.md"]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	got, _, err := r.selectNotes(t.Context(), store, query, nil)
	if err != nil || len(got) != 1 || got[0].Path != path {
		t.Fatalf("selectNotes = %+v, %v; want freeze.md", got, err)
	}
	const followUp = "Find recipes in note-00.md."
	got, _, err = r.selectNotes(t.Context(), store, followUp, nil)
	if err != nil || len(got) != 1 || memoryID(got[0]) != "note-00.md" {
		t.Fatalf("follow-up selection = %+v, %v", got, err)
	}
	calls := driver.Sent()
	if len(calls) != 2 || len(calls[0].Messages) != 3 || len(calls[1].Messages) != 3 ||
		!reflect.DeepEqual(calls[0].Messages[:2], calls[1].Messages[:2]) || calls[1].Messages[2].Text() != "User input:\n"+followUp {
		t.Fatal("changing only the question should keep both prior messages unchanged")
	}
	index := calls[0].Messages[0].Text()
	if strings.Count(index, "](") != 61 || !strings.Contains(index, "[freeze.md](freeze.md)") || strings.Contains(index, "Project policy") {
		t.Fatalf("expected all 61 file IDs, without the note body: %q", index)
	}
}

func TestRecallSearchesBeyondPreview(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		next         int
	}{
		{"lines", "# Memory\n" + strings.Repeat("\n", 199), 201},
		{"bytes", "# Memory\n" + strings.Repeat("x", 24_990) + "\n", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := system.MemoryStore(t.TempDir())
			const tail = "- [Release](release.md) — release freeze\n"
			writeRecallNote(t, string(store), "release.md", "No merges.")
			if err := os.WriteFile(filepath.Join(string(store), system.AutoMemoryIndexName), []byte(tc.prefix+tail), 0o600); err != nil {
				t.Fatal(err)
			}
			driver := aitest.New(aitest.Says(`{"action":"search_index","query":"release freeze"}`), aitest.Says(`{"selected_memories":["release.md"]}`))
			r := Recaller{Provider: compactRecordingProvider{driver}}
			got, _, err := r.selectNotes(t.Context(), store, "Can I merge?", nil)
			if err != nil || len(got) != 1 || memoryID(got[0]) != "release.md" {
				t.Fatalf("tail selection = %+v, %v", got, err)
			}
			calls := driver.Sent()
			blocks := calls[0].Messages[0].Content
			if len(blocks) != 2 || blocks[0].Text != tc.prefix || !strings.Contains(blocks[1].Text, fmt.Sprintf(`"line":%d`, tc.next)) || !strings.Contains(blocks[1].Text, "…") {
				t.Fatal("initial Recall preview did not preserve the raw prefix and separate the hint")
			}
			if len(calls[1].Tools) != 0 || !reflect.DeepEqual(calls[1].Messages[:3], calls[0].Messages) || !strings.Contains(calls[1].Messages[4].Text(), tail) {
				t.Fatal("Recall lookup did not append the result without changing the prefix")
			}
			t.Logf("Recall Aside · %s cutoff · user message 1 (raw preview abbreviated)\n%s\n[raw preview body omitted from terminal sample]%s\nUser message 2: %s\nUser message 3: %s", tc.name, strings.SplitN(blocks[0].Text, "\n", 2)[0], blocks[1].Text, calls[0].Messages[1].Text(), calls[0].Messages[2].Text())
		})
	}
}

func TestRecallIndexReflectsCatalogChanges(t *testing.T) {
	store := system.MemoryStore(t.TempDir())
	driver := aitest.Always(aitest.Says(`{"selected_memories":[]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	for _, desc := range []string{"release freeze", "new release policy"} {
		const file = "freeze.md"
		writeRecallNote(t, string(store), file, "Private note body")
		index := "- [Freeze](freeze.md) — " + desc + "\n"
		if err := os.WriteFile(filepath.Join(string(store), system.AutoMemoryIndexName), []byte(index), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.selectNotes(t.Context(), store, "Can I merge?", nil); err != nil {
			t.Fatal(err)
		}
		if got := driver.Last().Messages[0].Text(); got != index {
			t.Fatalf("preview did not preserve current index: %q", got)
		}
	}
}
