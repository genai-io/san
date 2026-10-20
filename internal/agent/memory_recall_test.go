package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
)

type recallProvider struct{ reply string }

func (p recallProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(aitest.Says(p.reply)).Client(), nil
}
func (recallProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (recallProvider) Name() string                                        { return "stub" }

// The selector's pick is attached by content: never a file it made up, never
// one already surfaced, nothing when its reply doesn't parse.
func TestRecall(t *testing.T) {
	user, project := t.TempDir(), t.TempDir()
	write := func(dir, name, desc string) string {
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

	r := Recaller{Provider: recallProvider{`{"selected_memories": ["project/freeze.md", "user/tabs.md", "user/nope.md"]}`}}
	got := r.Recall(context.Background(), stores, "can I merge?", map[string]bool{tabs: true})
	if len(got) != 1 || got[0].Path != freeze {
		t.Fatalf("Recall = %+v, want only freeze.md", got)
	}
	if out, _ := recallReminder(got); !strings.Contains(out, "body of freeze.md") || !strings.Contains(out, `source="memory-recall"`) {
		t.Errorf("reminder = %q", out)
	}
	if got := (Recaller{Provider: recallProvider{"no json"}}).Recall(context.Background(), stores, "q", nil); got != nil {
		t.Errorf("an unparsable reply recalled %+v", got)
	}
}

// A pick rides the first tool result after it lands, once, and never holds one
// up while it is still running.
func TestWithRecall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "freeze.md"), []byte("---\ndescription: release freeze\n---\nno merges"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Recaller{Provider: recallProvider{`{"selected_memories": ["project/freeze.md"]}`}}
	pick := r.Start(system.MemoryStores{Project: dir}, "can I merge?", nil)
	filter := WithRecall(nil, func() *Recall { return pick })
	call := func() string {
		res, err := filter(context.Background(), sdkagent.PostToolContext{Result: sdkagent.Result{Content: ai.TextContent("ok")}})
		if err != nil {
			t.Fatal(err)
		}
		if res == nil {
			return "ok"
		}
		return res.Content.Text()
	}
	<-pick.ready
	if got := call(); !strings.Contains(got, "no merges") || !strings.HasPrefix(got, "ok") {
		t.Errorf("first result after the pick = %q", got)
	}
	if got := call(); got != "ok" {
		t.Errorf("the pick rode twice: %q", got)
	}
	if got := pick.Attached(); len(got) != 1 || filepath.Base(got[0]) != "freeze.md" {
		t.Errorf("Attached = %q", got)
	}
	if (Recaller{}).Start(system.MemoryStores{Project: dir}, "q", nil) != nil {
		t.Error("a recaller with no model started a pick")
	}
}
