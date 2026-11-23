package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/automemory"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
	"gopkg.in/yaml.v3"
)

type memoryDocsRequest struct {
	System   string   `yaml:"System"`
	Tools    []string `yaml:"Tools"`
	Messages []struct {
		Role    ai.Role
		Content yaml.Node
	} `yaml:"Messages"`
}

func TestAutoMemoryDocsExamples(t *testing.T) {
	doc, err := os.ReadFile("../../docs/concepts/auto-memory.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "\n## Example Aside requests\n")
	section, _, ended := strings.Cut(section, "\n## Settings and inspection\n")
	if !ok || !ended {
		t.Fatal("Auto Memory example section not found")
	}
	blocks := regexp.MustCompile("(?ms)^```yaml\n(.*?)^```$").FindAllStringSubmatch(section, -1)
	if len(blocks) != 3 {
		t.Fatalf("found %d YAML examples, want Recall, Update, and Write calls", len(blocks))
	}
	decode := func(i int, out any) {
		t.Helper()
		dec := yaml.NewDecoder(strings.NewReader(blocks[i][1]))
		dec.KnownFields(true)
		if err := dec.Decode(out); err != nil {
			t.Fatalf("YAML example %d: %v", i+1, err)
		}
	}
	var recall, update memoryDocsRequest
	var writes struct {
		ToolCalls []struct {
			Name      string
			Arguments map[string]string
		} `yaml:"ToolCalls"`
	}
	decode(0, &recall)
	decode(1, &update)
	decode(2, &writes)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := system.MemoryStore(dir)
	const (
		note      = "archived-demo-recordings-label.md"
		name      = "Archived demo recordings: indigo swan 2826"
		desc      = "In weekly team summaries, label archived demo recordings indigo swan 2826."
		indexLine = "- [" + name + "](" + note + ") — (project) " + desc
		question  = "What label do we use for archived demo recordings in weekly team summaries?"
	)
	indexPath := filepath.Join(dir, system.AutoMemoryIndexName)
	writeTestFile(t, indexPath, indexLine+"\n")
	writeTestFile(t, filepath.Join(dir, note), "---\nname: '"+name+"'\ndescription: "+desc+"\ntype: project\n---\n"+desc+"\n")

	provider := aitest.New(aitest.Says(`{"selected_memories":["` + note + `"]}`))
	recaller := agent.Recaller{Provider: staticTestProvider{provider.Client()}, Model: "test"}
	if _, err := recaller.Prepare(t.Context(), store, core.UserMessage(question, nil), nil); err != nil {
		t.Fatal(err)
	}
	if provider.Calls() != 1 {
		t.Fatalf("Recall made %d SDK requests, want 1", provider.Calls())
	}
	admitted := provider.Last()
	if admitted.System != strings.TrimSuffix(recall.System, "\n") {
		t.Errorf("Recall system prompt:\ngot:  %q\nwant: %q", admitted.System, recall.System)
	}
	if len(recall.Tools) != 0 || len(admitted.Tools) != 0 || len(admitted.Messages) != len(recall.Messages) {
		t.Fatalf("Recall tools/messages differ from the example: got %d tools and %d messages", len(admitted.Tools), len(admitted.Messages))
	}
	for i, want := range recall.Messages {
		got := admitted.Messages[i]
		if want.Content.Kind != yaml.ScalarNode || got.Role != want.Role || len(got.Content) != 1 ||
			got.Content[0].Type != ai.BlockText || strings.TrimSuffix(got.Text(), "\n") != strings.TrimSuffix(want.Content.Value, "\n") {
			t.Errorf("Recall message %d: got %s %q, want %s %q", i+1, got.Role, got.Text(), want.Role, want.Content.Value)
		}
	}

	// The example abbreviates the main system prompt and shows only tool names.
	if update.System != "<main session system prompt, unchanged>" || len(update.Messages) != 3 ||
		update.Messages[0].Role != ai.RoleUser || update.Messages[1].Role != ai.RoleAssistant ||
		update.Messages[0].Content.Kind != yaml.ScalarNode || update.Messages[1].Content.Kind != yaml.ScalarNode {
		t.Fatal("Update example must show the main prompt placeholder and a completed turn before the review message")
	}
	prefix := &ai.Request{
		System: "Main system prompt for the documentation fixture.",
		Messages: []ai.Message{
			core.UserMessage(strings.TrimSuffix(update.Messages[0].Content.Value, "\n"), nil),
			core.AssistantMessage(update.Messages[1].Content.Value, "", nil),
		},
	}
	for _, name := range update.Tools {
		prefix.Tools = append(prefix.Tools, ai.Schema{Name: name})
	}
	if len(writes.ToolCalls) != 2 {
		t.Fatalf("found %d documented writes, want 2", len(writes.ToolCalls))
	}
	call := func(id, name string, args map[string]string) ai.ToolCall {
		t.Helper()
		input, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return ai.ToolCall{ID: id, Name: name, Input: string(input)}
	}
	var writeCalls []ai.ToolCall
	var saved []string
	for i, write := range writes.ToolCalls {
		file := write.Arguments["file_path"]
		if write.Name != "Write" || file == "" || write.Arguments["content"] == "" {
			t.Fatalf("invalid documented Write call: %+v", write)
		}
		writeCalls = append(writeCalls, call(fmt.Sprintf("write-%d", i), write.Name, write.Arguments))
		saved = append(saved, "saved "+file)
	}
	driver := aitest.New(aitest.Asks(
		call("read-index", "Read", map[string]string{"file_path": system.AutoMemoryIndexName}),
		call("read-note", "Read", map[string]string{"file_path": note}),
	), aitest.Asks(writeCalls...))
	updater := automemory.Updater{Provider: staticTestProvider{driver.Client()}, Model: "test"}
	change, err := updater.Propose(t.Context(), store, prefix)
	if err != nil || change == nil {
		t.Fatalf("replay Update: change=%v, err=%v", change, err)
	}
	defer change.Discard()
	requests := driver.Sent()
	if len(requests) != 2 {
		t.Fatalf("Update made %d SDK requests, want 2", len(requests))
	}
	for i, req := range requests {
		if len(req.Messages) != len(update.Messages)+2*i || req.System != prefix.System ||
			!reflect.DeepEqual(req.Tools, prefix.Tools) || !reflect.DeepEqual(req.Messages[:2], prefix.Messages) {
			t.Fatalf("Update request %d: expected the unchanged main prefix and review suffix", i+1)
		}
	}
	want := update.Messages[2]
	var blocksShown []struct{ Type, Text string }
	if err := want.Content.Decode(&blocksShown); err != nil {
		t.Fatal(err)
	}
	got := requests[0].Messages[2]
	if got.Role != want.Role || len(got.Content) != len(blocksShown) {
		t.Fatalf("Update review: got %s with %d blocks, want %s with %d blocks", got.Role, len(got.Content), want.Role, len(blocksShown))
	}
	for i, block := range blocksShown {
		text := strings.NewReplacer(indexPath, "/example/project-memory/MEMORY.md", dir, "/example/project-memory").Replace(got.Content[i].Text)
		if got.Content[i].Type != ai.BlockText || block.Type != "text" || text != strings.TrimSuffix(block.Text, "\n") {
			t.Errorf("Update review block %d:\ngot:  %q\nwant: %q", i+1, text, block.Text)
		}
	}

	notice, err := change.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := "Memory · " + strings.Join(saved, ", "); notice != want || !strings.Contains(section, "`"+notice+"`") {
		t.Errorf("Update notice %q: want %q and a matching notice in the docs", notice, want)
	}
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), indexLine) {
		t.Error("Update removed the original index entry")
	}
	for _, write := range writes.ToolCalls {
		file := write.Arguments["file_path"]
		if !strings.Contains(string(index), "]("+file+") — (project) ") {
			t.Errorf("Update did not index %s as a project note:\n%s", file, index)
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || strings.TrimSuffix(string(data), "\n") != strings.TrimSuffix(write.Arguments["content"], "\n") {
			t.Errorf("saved %s differs from documented content: %q (err=%v)", file, data, err)
		}
	}
}
