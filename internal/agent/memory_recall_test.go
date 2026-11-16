package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool/fs"
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
	got, err := r.Recall(context.Background(), stores, "can I merge?", map[string]bool{tabs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != freeze {
		t.Fatalf("Recall = %+v, want only freeze.md", got)
	}
	if out, _ := recallReminder(got); !strings.Contains(out, "body of freeze.md") || !strings.Contains(out, `source="memory-recall"`) {
		t.Errorf("reminder = %q", out)
	}
	if got, err := (Recaller{Provider: recallProvider{"no json"}}).Recall(context.Background(), stores, "q", nil); got != nil || err != nil {
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

func TestWithRecallWaitsForParallelReads(t *testing.T) {
	dir := t.TempDir()
	var memories []system.Memory
	var calls []ai.ToolCall
	for i, name := range []string{"signoff", "signatures", "delivery", "concise"} {
		path := filepath.Join(dir, name+".md")
		if err := os.WriteFile(path, []byte("memory body: "+name), 0600); err != nil {
			t.Fatal(err)
		}
		if i != 0 {
			memories = append(memories, system.Memory{Path: path, Store: "project"})
		}
		if i != 3 {
			input, err := json.Marshal(map[string]string{"file_path": path})
			if err != nil {
				t.Fatal(err)
			}
			calls = append(calls, ai.ToolCall{ID: name, Name: "Read", Input: string(input)})
		}
	}
	pick := &Recall{ready: make(chan struct{}), memories: memories}
	close(pick.ready)
	filter := WithRecall(nil, func() *Recall { return pick })
	batch := core.AssistantMessage("", "", calls)
	batch.ID = "parallel-reads"
	// The selector is ready as the first Read finishes; the other reads have
	// not recorded their views yet, but will join the same model-facing batch.
	for i, call := range []ai.ToolCall{calls[0], calls[2], calls[1]} {
		params, err := core.ParseToolInput(call.Input)
		if err != nil {
			t.Fatal(err)
		}
		result := (&fs.ReadTool{}).Execute(t.Context(), params, dir)
		if !result.Success {
			t.Fatal(result)
		}
		out, err := filter(t.Context(), sdkagent.PostToolContext{
			Call: call, Messages: []core.Message{batch}, Result: sdkagent.TextResult(result.FormatForLLM()),
		})
		if err != nil {
			t.Fatal(err)
		}
		if i < len(calls)-1 {
			if out != nil || len(pick.Attached()) != 0 {
				t.Fatal("recall attached while the batch still had unfinished reads")
			}
			continue
		}
		if out == nil || !strings.Contains(out.Content.Text(), "memory body: concise") {
			t.Fatal("unread memory was not attached to the completed batch")
		}
		for _, name := range []string{"signatures", "delivery"} {
			if strings.Contains(out.Content.Text(), "Memory `"+filepath.Join(dir, name+".md")+"`") {
				t.Fatalf("explicitly read memory %s was recalled again", name)
			}
		}
	}
	if paths := pick.Attached(); len(paths) != 1 || filepath.Base(paths[0]) != "concise.md" {
		t.Fatalf("Attached = %v, want only concise.md", paths)
	}
}

func TestWithRecallAcrossBatches(t *testing.T) {
	for _, state := range []string{"abandoned", "completed"} {
		t.Run(state, func(t *testing.T) {
			finishFirst := state == "completed"
			path := filepath.Join(t.TempDir(), "memory.md")
			if err := os.WriteFile(path, []byte("recalled fact"), 0600); err != nil {
				t.Fatal(err)
			}
			pick := &Recall{ready: make(chan struct{}), memories: []system.Memory{{Path: path, Store: "project"}}}
			filtered := 0
			filter := WithRecall(func(_ context.Context, c sdkagent.PostToolContext) (*sdkagent.Result, error) {
				filtered++
				r := sdkagent.TextResult("filtered " + c.Call.ID)
				return &r, nil
			}, func() *Recall { return pick })
			apply := func(batch core.Message, call ai.ToolCall, wantRecall bool) {
				t.Helper()
				out, err := filter(t.Context(), sdkagent.PostToolContext{Call: call, Messages: []core.Message{batch}})
				if err != nil || out == nil {
					t.Fatalf("filter = %v, %v", out, err)
				}
				if text := out.Content.Text(); !strings.HasPrefix(text, "filtered "+call.ID) || strings.Contains(text, "recalled fact") != wantRecall {
					t.Fatalf("result = %q, want recall = %v", text, wantRecall)
				}
			}
			calls := []ai.ToolCall{{ID: "a", Name: "Bash"}, {ID: "b", Name: "Bash"}}
			first := core.AssistantMessage("", "", calls)
			first.ID = "first"
			apply(first, calls[0], false)
			if finishFirst {
				apply(first, calls[1], false)
			}
			close(pick.ready)
			calls = []ai.ToolCall{{ID: "c", Name: "Read"}, {ID: "d", Name: "Bash"}}
			second := core.AssistantMessage("", "", calls)
			second.ID = "second"
			apply(second, calls[1], false)
			apply(second, calls[0], true)
			wantFiltered := 3
			if finishFirst {
				wantFiltered++
			}
			if filtered != wantFiltered {
				t.Fatalf("filtered %d results, want %d", filtered, wantFiltered)
			}
		})
	}
}
