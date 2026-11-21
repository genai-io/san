package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRecallSelectNotes(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	write := func(dir, name, desc string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("---\nname: x\ndescription: "+desc+"\n---\nbody of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tabs := write(user, "tabs.md", "tabs over spaces")
	freeze := write(project, "freeze.md", "release freeze")
	write(project, "MEMORY.md", "- [Freeze](freeze.md)")
	stores := system.MemoryStores{User: user, Project: project}

	driver := aitest.New(aitest.Says(`{"selected_memories":["project/freeze.md","user/tabs.md","user/nope.md","project/MEMORY.md","project/freeze.md"]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	got, err := r.selectNotes(t.Context(), stores, "can I merge?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != freeze || got[1].Path != tabs {
		t.Fatalf("selectNotes = %+v, want freeze.md and tabs.md", got)
	}
}

func TestRecallRanksBothStoresBeforeCap(t *testing.T) {
	stores := system.MemoryStores{User: t.TempDir(), Project: t.TempDir()}
	for i := range 60 {
		path := filepath.Join(stores.User, fmt.Sprintf("note-%02d.md", i))
		if err := os.WriteFile(path, []byte("---\ndescription: cooking recipes\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(stores.Project, "freeze.md")
	if err := os.WriteFile(path, []byte("Project policy"), 0o644); err != nil {
		t.Fatal(err)
	}
	const query = "Check `project/freeze.md`, please."
	candidates := recallCandidates(t.Context(), stores, query)
	if len(candidates) != 60 || candidates[0].Path != path {
		t.Fatalf("got %d candidates, want 60 with project/freeze.md first", len(candidates))
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["project/freeze.md"]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}}
	got, err := r.selectNotes(t.Context(), stores, query, nil)
	if err != nil || len(got) != 1 || got[0].Path != path {
		t.Fatalf("selectNotes = %+v, %v; want project/freeze.md", got, err)
	}
}
