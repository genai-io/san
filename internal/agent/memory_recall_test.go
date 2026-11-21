package agent

import (
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
