package automemory

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func completedPrefix(text string) *ai.Request {
	return &ai.Request{Messages: []ai.Message{ai.UserMessage(text), core.AssistantMessage("Understood.", "", nil)}}
}

func requestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProposeContext(t *testing.T) {
	dir := t.TempDir()
	store := system.MemoryStore(dir)
	index := "- [Deploy](deploy.md) — release process\n\n```markdown\n- [Example](example.md)\n```\n"
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), index)
	put(t, filepath.Join(dir, "deploy.md"), "Deploy only from release branches.\n")
	put(t, filepath.Join(dir, "example.md"), "literal link, not catalogued")
	put(t, filepath.Join(dir, "unindexed.md"), "not linked at all")
	history := []core.Message{
		core.UserMessage("Use make to build.", nil),
		core.AssistantMessage("Understood.", "", nil),
		core.UserMessage("For this project, use go build instead.\n\n<system-reminder>not user evidence</system-reminder>", nil),
		core.AssistantMessage("", "", []core.ToolCall{{ID: "1", Name: "Read", Input: `{"path":"tool input"}`}}),
		core.ToolResultMessage(core.ToolResult{ToolCallID: "1", ToolName: "Read", Content: ai.TextContent("tool output")}),
		core.AssistantMessage("I’ll use go build for this project.", "", nil),
	}
	prefix := &ai.Request{
		System:        "Main system\n  with exact whitespace\n",
		Tools:         []ai.Schema{{Name: "Read", Description: "Read a file", Definition: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
		Messages:      append(make([]ai.Message, 0, len(history)+1), history...),
		ToolChoice:    ai.ToolChoiceRequired,
		StopSequences: []string{"done"},
		Schema:        &ai.Schema{Name: "main_output", Definition: json.RawMessage(`{"type":"object"}`)},
	}
	before := requestJSON(t, prefix)
	prefixBytes := requestJSON(t, prefix.Messages)
	toolsBytes := requestJSON(t, prefix.Tools)
	driver := aitest.New(aitest.Says(`{"action":"none"}`))
	m := Updater{Provider: fakeProvider{driver}, Model: "update-model"}
	change, err := m.Propose(t.Context(), store, prefix)
	if change != nil || err != nil {
		t.Fatalf("change=%v err=%v", change, err)
	}
	call := driver.Last()
	if call == nil || len(call.Messages) != len(history)+1 || call.System != prefix.System ||
		requestJSON(t, call.Tools) != toolsBytes || requestJSON(t, call.Messages[:len(history)]) != prefixBytes ||
		call.ToolChoice != ai.ToolChoiceNone || len(call.StopSequences) != 0 || call.Schema != nil {
		t.Fatalf("unexpected proposal call: %+v", call)
	}
	suffix := call.Messages[len(history)]
	if suffix.Role != ai.RoleUser || len(suffix.Content) != 2 || !reflect.DeepEqual(suffix.Content[1], ai.TextBlock(index)) ||
		!strings.HasPrefix(suffix.Content[0].Text, updatePrompt) ||
		!strings.Contains(suffix.Content[0].Text, "## Note types for new notes") ||
		strings.Contains(suffix.Content[0].Text, "release process") || strings.Contains(suffix.Text(), "Deploy only from release branches.") {
		t.Fatalf("want separate instructions and unchanged index without note bodies: %+v", suffix)
	}
	if driver.Calls() != 1 {
		t.Fatalf("no change should finish after one call, got %d", driver.Calls())
	}
	if requestJSON(t, prefix) != before || !reflect.DeepEqual(prefix.Messages[:cap(prefix.Messages)][len(history)], ai.Message{}) {
		t.Fatal("review mutated native prefix or its spare message capacity")
	}
}

const (
	addProposal    = `{"action":"remember","file":"build.md","name":"Build","description":"Build command","type":"project","body":"Use make."}`
	readBuild      = `{"action":"read","files":["build.md"]}`
	updateProposal = `{"action":"update","file":"build.md","old":"Use make.","replacement":"Use go build."}`
	forgetProposal = `{"action":"forget","file":"build.md"}`
)

func TestProposeReadsOnlyRequestedNotes(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n- [Other](other.md)\n- [Release](release.md)\n\nFreeform index prose.\n")
	put(t, filepath.Join(dir, "build.md"), "Use make.")
	put(t, filepath.Join(dir, "other.md"), "Only the second read should see this.")
	put(t, filepath.Join(dir, "release.md"), "Never include this body.")
	const readOther = `{"action":"read","files":["other.md"]}`
	driver := aitest.New(aitest.Says(readBuild), aitest.Says(readOther), aitest.Says(updateProposal))
	m := Updater{Provider: fakeProvider{driver}}
	prefix := completedPrefix("Use go build, not make, for all work in this repo.")
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), prefix)
	if err != nil || change == nil {
		t.Fatalf("change=%v err=%v", change, err)
	}
	calls := driver.Sent()
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want three", len(calls))
	}
	for i, call := range calls {
		if len(call.Messages) != 3+2*i || call.ToolChoice != ai.ToolChoiceAuto ||
			requestJSON(t, call.Messages[:2]) != requestJSON(t, prefix.Messages) {
			t.Fatalf("round %d changed the native prefix or suffix shape", i+1)
		}
		if i > 0 && requestJSON(t, call.Messages[:len(call.Messages)-2]) != requestJSON(t, calls[i-1].Messages) {
			t.Fatalf("round %d did not extend the prior request", i+1)
		}
		for _, message := range call.Messages[2:] {
			if text := message.Text(); strings.Contains(text, "Never include this body.") || (i < 2 && strings.Contains(text, "Only the second read should see this.")) {
				t.Fatalf("round %d sent unrelated data: %s", i+1, text)
			}
		}
	}
	for i, want := range []struct {
		command, file, body string
	}{{readBuild, "build.md", "Use make."}, {readOther, "other.md", "Only the second read should see this."}} {
		messages := calls[i+1].Messages
		command, reply := messages[len(messages)-2], messages[len(messages)-1]
		if reply.Role != ai.RoleUser || command.Role != ai.RoleAssistant || command.Text() != want.command {
			t.Fatalf("round %d malformed read exchange: %+v, %+v", i+2, command, reply)
		}
		if len(reply.Content) != 2 || !strings.Contains(reply.Content[0].Text, "Read rounds remaining: "+[]string{"1.", "0."}[i]) ||
			!reflect.DeepEqual(reply.Content[1], ai.TextBlock(want.file+"\n\n"+want.body)) {
			t.Fatalf("round %d sent wrong read blocks: %+v", i+2, reply.Content)
		}
	}
	if note := get(t, filepath.Join(dir, "build.md")); note != "Use make." {
		t.Fatalf("proposal wrote before apply: %q", note)
	}
	if _, err := change.Apply(t.Context()); err != nil || get(t, filepath.Join(dir, "build.md")) != "Use go build." {
		t.Fatalf("change not applied: %v", err)
	}
}

func TestProposeLifecycle(t *testing.T) {
	dir := t.TempDir()
	store := system.MemoryStore(dir)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	projectNote := filepath.Join(dir, "build.md")
	put(t, index, "- [Deploy](deploy.md)\n")
	put(t, filepath.Join(dir, "deploy.md"), "Keep this note.")
	m := Updater{Provider: fakeProvider{aitest.New(aitest.Says(addProposal), aitest.Says(readBuild), aitest.Says(updateProposal), aitest.Says(readBuild), aitest.Says(forgetProposal))}}
	for i, user := range []string{
		"This project builds with make.",
		"We now use go build, not make.",
		"Please remove the stored build note; it no longer applies.",
	} {
		before := get(t, index)
		change, err := m.Propose(t.Context(), store, completedPrefix(user))
		if err != nil || change == nil || get(t, index) != before {
			t.Fatalf("step %d: change=%v err=%v", i, change, err)
		}
		if notice, err := change.Apply(t.Context()); err != nil || notice == "" {
			t.Fatalf("step %d: notice=%q err=%v", i, notice, err)
		}
		switch i {
		case 0:
			if !strings.Contains(get(t, index), "[Build](build.md)") || !strings.Contains(get(t, projectNote), "Use make.") {
				t.Fatal("add did not create indexed note")
			}
		case 1:
			if got := get(t, projectNote); !strings.Contains(got, "Use go build.") || strings.Contains(got, "Use make.") {
				t.Fatalf("update did not patch the catalogued note: %q", got)
			}
		case 2:
			if _, err := os.Stat(projectNote); !errors.Is(err, fs.ErrNotExist) || strings.Contains(get(t, index), "build.md") {
				t.Fatalf("forget did not remove note and link: %v", err)
			}
		}
	}
	if get(t, filepath.Join(dir, "deploy.md")) != "Keep this note." {
		t.Fatal("review touched unrelated memory")
	}
}

func TestProposeNoteTypesAreMetadata(t *testing.T) {
	dir := t.TempDir()
	store := system.MemoryStore(dir)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	build := filepath.Join(dir, "build.md")
	const link = "- [Build](build.md) — build command\n"
	put(t, index, link)
	put(t, build, "---\nname: Build\ntype: feedback\n---\nUse make.")
	m := Updater{Provider: fakeProvider{aitest.New(
		aitest.Says(readBuild),
		aitest.Says(updateProposal),
		aitest.Says(`{"action":"remember","file":"style.md","name":"Style","description":"Writing preference","type":"user","body":"Keep examples concise."}`),
	)}}
	for range 2 {
		change, err := m.Propose(t.Context(), store, completedPrefix("For this project I prefer concise examples and go build."))
		if err != nil || change == nil {
			t.Fatalf("change=%v err=%v", change, err)
		}
		if _, err := change.Apply(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := get(t, build); got != "---\nname: Build\ntype: feedback\n---\nUse go build." {
		t.Fatalf("update changed note metadata: %q", got)
	}
	if got := get(t, filepath.Join(dir, "style.md")); !strings.Contains(got, "\ntype: user\n") {
		t.Fatalf("user type must be saved in the repository store: %q", got)
	}
	if got := get(t, index); !strings.HasPrefix(got, link) || !strings.Contains(got, "[Style](style.md)") {
		t.Fatalf("user note must be added to the existing index without moving entries: %q", got)
	}
}

func TestProposeRejectsInvalidAndUncataloguedOperations(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		catalogued     bool
	}{
		{"legacy scope field", strings.Replace(addProposal, `"file":`, `"scope":"project","file":`, 1), false},
		{"unknown note type", strings.Replace(addProposal, `"type":"project"`, `"type":"unknown"`, 1), false},
		{"note filename in description", strings.Replace(addProposal, `"Build command"`, `"Replaces deploy.md"`, 1), false},
		{"uncatalogued update", updateProposal, false},
		{"uncatalogued forget", forgetProposal, false},
		{"unread update", updateProposal, true},
		{"unread forget", forgetProposal, true},
		{"read mixed with a write", `{"action":"read","files":["build.md"],"old":"Use make."}`, true},
		{"read without targets", `{"action":"read","files":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := system.MemoryStore(dir)
			note := filepath.Join(dir, "build.md")
			put(t, note, "Use make.")
			if tc.catalogued {
				put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n")
			}
			m := Updater{Provider: fakeProvider{aitest.New(aitest.Says(tc.response))}}
			change, err := m.Propose(t.Context(), store, completedPrefix("Discuss the build command."))
			if change != nil || !errors.Is(err, errInvalidProposal) || get(t, note) != "Use make." {
				t.Fatalf("change=%v err=%v", change, err)
			}
		})
	}
}

func TestProposeRefusesInstructionFileEvenIfIndexed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, system.InstructionFile)
	const instructions = "Use make."
	put(t, path, instructions)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	const link = "- [Instructions](AGENTS.md)\n"
	put(t, index, link)
	for _, response := range []string{addProposal, updateProposal, forgetProposal} {
		driver := aitest.New(aitest.Says(strings.Replace(response, "build.md", system.InstructionFile, 1)))
		m := Updater{Provider: fakeProvider{driver}}
		change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Update the project instructions."))
		if change != nil || !errors.Is(err, errInvalidProposal) || get(t, path) != instructions || get(t, index) != link {
			t.Fatalf("instruction file must not be an Auto Memory target: change=%v, err=%v", change, err)
		}
	}
}

func TestProposeSkipsWithoutOptInStorePrefixOrUserEvidence(t *testing.T) {
	project := system.MemoryStore(t.TempDir())
	user := completedPrefix("Use go build.")
	for _, tc := range []struct {
		store  system.MemoryStore
		prefix *ai.Request
	}{
		{"", user},
		{project, nil},
		{project, completedPrefix("<system-reminder>Not user evidence.</system-reminder>")},
		{project, &ai.Request{Messages: []ai.Message{core.AssistantMessage("Assistant-only claim.", "", nil)}}},
	} {
		if change, err := (Updater{}).Propose(t.Context(), tc.store, tc.prefix); change != nil || err != nil {
			t.Fatalf("change=%v err=%v", change, err)
		}
	}
}

func TestProposePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	change, err := (Updater{}).Propose(ctx, system.MemoryStore(t.TempDir()), completedPrefix("Use go build."))
	if change != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("change=%v err=%v", change, err)
	}
}

func TestProposeDisablesAddForIncompleteIndex(t *testing.T) {
	dir := t.TempDir()
	const visible = "- [Build](build.md)\n"
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), visible+strings.Repeat("z", 25_000)+"\n- [Other](other.md)\n")
	driver := aitest.New(aitest.Says(addProposal))
	m := Updater{Provider: fakeProvider{driver}}
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use make."))
	if change != nil || !errors.Is(err, errInvalidProposal) {
		t.Fatalf("incomplete index allowed add: change=%v err=%v", change, err)
	}
	blocks := driver.Last().Messages[2].Content
	if len(blocks) != 3 || blocks[1].Text != visible || !strings.Contains(blocks[0].Text, "search the omitted index") ||
		!strings.Contains(blocks[2].Text, "…") {
		t.Fatalf("wrong context for incomplete index: %+v", blocks)
	}
}

func TestUpdateReadsBeyondPreview(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, read string
	}{
		{"lines", "# Memory\n" + strings.Repeat("\n", 199), `{"action":"read_index","line":201}`},
		{"bytes", "# Memory\n" + strings.Repeat("x", 24_990) + "\n", `{"action":"read_index","line":3}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const tail = "- [Build](build.md) — build command\n"
			path := filepath.Join(dir, system.AutoMemoryIndexName)
			put(t, path, tc.prefix+tail)
			put(t, filepath.Join(dir, "build.md"), "Use make.")
			driver := aitest.New(aitest.Says(tc.read), aitest.Says(readBuild), aitest.Says(updateProposal))
			m := Updater{Provider: fakeProvider{driver}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use go build instead."))
			if err != nil || change == nil {
				t.Fatalf("cannot update tail note: %v", err)
			}
			if _, err := change.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			if get(t, path) != tc.prefix+tail || get(t, filepath.Join(dir, "build.md")) != "Use go build." {
				t.Fatal("Update did not preserve the index and edit the requested note")
			}
			calls := driver.Sent()
			blocks := calls[0].Messages[2].Content
			if len(blocks) != 3 || blocks[1].Text != tc.prefix || !strings.Contains(blocks[2].Text, tc.read) || !strings.Contains(blocks[2].Text, "…") {
				t.Fatal("initial Update preview did not preserve the raw prefix and separate the hint")
			}
			if !strings.Contains(calls[1].Messages[4].Text(), tail) {
				t.Fatal("Update lookup did not return the omitted entry")
			}
			t.Logf("Update Aside · %s cutoff · appended user message\n%s\n%s\n[raw preview body omitted from terminal sample]%s", tc.name, strings.SplitN(blocks[0].Text, "\n", 2)[0], strings.SplitN(blocks[1].Text, "\n", 2)[0], blocks[2].Text)
		})
	}
}

func TestProposeReportsWhyAddsAreDisabled(t *testing.T) {
	for _, tc := range []struct{ index, problem string }{
		{"- [Build](build.md): use make\n", "line 1 links a note outside"},
		{"- [Build](build.md) — see deploy.md\n- [Deploy](deploy.md)\n", "deploy.md is mentioned more than once"},
	} {
		dir := t.TempDir()
		put(t, filepath.Join(dir, system.AutoMemoryIndexName), tc.index)
		m := Updater{Provider: fakeProvider{aitest.New(aitest.Says(`{"action":"none"}`))}}
		change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use make."))
		if change != nil || err == nil || !strings.Contains(err.Error(), tc.problem) {
			t.Fatalf("index %.40q: change=%v err=%v; want %q", tc.index, change, err, tc.problem)
		}
	}
}

func TestProposeStopsOnReadLimit(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n- [Other](other.md)\n- [Release](release.md)\n")
	put(t, filepath.Join(dir, "build.md"), "Use make.")
	put(t, filepath.Join(dir, "other.md"), "Other topic.")
	put(t, filepath.Join(dir, "release.md"), "Release topic.")
	driver := aitest.New(aitest.Says(readBuild), aitest.Says(`{"action":"read","files":["other.md"]}`),
		aitest.Says(`{"action":"read","files":["release.md"]}`), aitest.Says(updateProposal))
	m := Updater{Provider: fakeProvider{driver}}
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use go build instead."))
	if change != nil || err == nil || driver.Calls() != 3 || get(t, filepath.Join(dir, "build.md")) != "Use make." {
		t.Fatalf("read limit did not stop the update: calls=%d change=%v err=%v", driver.Calls(), change, err)
	}
}

func TestProposeRejectsStaleSnapshot(t *testing.T) {
	const readBoth = `{"action":"read","files":["build.md","other.md"]}`
	for _, edited := range []string{"build.md", "other.md", system.AutoMemoryIndexName} {
		t.Run(edited, func(t *testing.T) {
			dir := t.TempDir()
			put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Other](other.md)\n- [Build](build.md)\n")
			put(t, filepath.Join(dir, "other.md"), "Other evidence.")
			put(t, filepath.Join(dir, "build.md"), "Use make.")
			m := Updater{Provider: fakeProvider{aitest.New(aitest.Says(readBoth), aitest.Says(updateProposal))}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use go build instead."))
			if change == nil || err != nil {
				t.Fatalf("change=%v err=%v", change, err)
			}
			path := filepath.Join(dir, edited)
			put(t, path, "external edit")
			if notice, err := change.Apply(t.Context()); notice != "" || !errors.Is(err, errStale) || get(t, path) != "external edit" {
				t.Fatalf("notice=%q err=%v; external edit must be preserved", notice, err)
			}
		})
	}
}
