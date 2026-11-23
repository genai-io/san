package agent

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
	got, err := r.selectNotes(t.Context(), store, "can I merge?", nil, nil)
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
	got, err := r.selectNotes(t.Context(), store, query, nil, nil)
	if err != nil || len(got) != 1 || got[0].Path != path {
		t.Fatalf("selectNotes = %+v, %v; want freeze.md", got, err)
	}
	const followUp = "Find recipes in note-00.md."
	got, err = r.selectNotes(t.Context(), store, followUp, nil, nil)
	if err != nil || len(got) != 1 || memoryID(got[0]) != "note-00.md" {
		t.Fatalf("follow-up selection = %+v, %v", got, err)
	}
	calls := driver.Sent()
	if len(calls) != 2 || len(calls[0].Messages) != 3 || len(calls[1].Messages) != 3 ||
		!reflect.DeepEqual(calls[0].Messages[:2], calls[1].Messages[:2]) || calls[1].Messages[2].Text() != followUp {
		t.Fatal("changing only the question should keep both prior messages unchanged")
	}
	index := calls[0].Messages[0].Text()
	if strings.Count(index, "\n- ") != 61 || !strings.Contains(index, "freeze.md:") || strings.Contains(index, "Project policy") {
		t.Fatalf("expected all 61 file IDs, without the note body: %q", index)
	}
}

func TestRecallIndexReflectsCatalogChanges(t *testing.T) {
	store := system.MemoryStore(t.TempDir())
	driver := aitest.Always(aitest.Says(`{"selected_memories":[]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	for _, desc := range []string{"release freeze", "new release policy"} {
		writeRecallNote(t, string(store), "freeze.md", "---\ndescription: "+desc+"\n---\nPrivate note body")
		if _, err := r.selectNotes(t.Context(), store, "Can I merge?", nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := driver.Last().Messages[0].Text(); got != "Memory files:\n- freeze.md: "+desc+"\n" {
			t.Fatalf("index did not reflect current metadata: %q", got)
		}
	}
}
