package automemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func completedPrefix(text string) *ai.Request {
	return &ai.Request{
		Messages: []ai.Message{ai.UserMessage(text), core.AssistantMessage("Understood.", "", nil)},
		Tools:    []ai.Schema{{Name: tool.ToolRead}, {Name: tool.ToolWrite}, {Name: tool.ToolEdit}, {Name: tool.ToolBash}},
	}
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
	driver := aitest.New(aitest.Says(noMemoryChanges))
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
	if suffix.Role != ai.RoleUser || len(suffix.Content) != 3 || !reflect.DeepEqual(suffix.Content[2], ai.TextBlock(index)) ||
		!strings.HasPrefix(suffix.Content[0].Text, updatePrompt) || !strings.HasPrefix(suffix.Content[1].Text, "# Project memory layout") ||
		strings.Contains(suffix.Text(), "# Note format example") ||
		strings.Contains(suffix.Content[0].Text, "release process") || strings.Contains(suffix.Text(), "Deploy only from release branches.") {
		t.Fatalf("want separate instructions and unchanged index without note bodies: %+v", suffix)
	}
	for _, kind := range []string{"user", "feedback", "project", "reference"} {
		if !strings.Contains(suffix.Content[1].Text, "\n- "+kind+": ") {
			t.Errorf("memory layout does not explain %q notes", kind)
		}
	}
	if driver.Calls() != 1 {
		t.Fatalf("no change should finish after one call, got %d", driver.Calls())
	}
	if requestJSON(t, prefix) != before || !reflect.DeepEqual(prefix.Messages[:cap(prefix.Messages)][len(history)], ai.Message{}) {
		t.Fatal("review mutated native prefix or its spare message capacity")
	}
}

var (
	addProposal    = ai.ToolCall{ID: "add", Name: tool.ToolWrite, Input: `{"file_path":"build.md","content":"---\nname: Build\ndescription: Build command\ntype: project\n---\nUse make."}`}
	readBuild      = readNote("build.md")
	updateProposal = ai.ToolCall{ID: "edit", Name: tool.ToolEdit, Input: `{"file_path":"build.md","old_string":"Use make.","new_string":"Use go build."}`}
	forgetProposal = ai.ToolCall{ID: "forget", Name: tool.ToolBash, Input: `{"command":"rm build.md"}`}
)

func readNote(file string) ai.ToolCall {
	return ai.ToolCall{ID: file, Name: tool.ToolRead, Input: `{"file_path":"` + file + `"}`}
}

func TestProposeReadsOnlyRequestedNotes(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n- [Other](other.md)\n- [Release](release.md)\n\nFreeform index prose.\n")
	put(t, filepath.Join(dir, "build.md"), "Use make.")
	put(t, filepath.Join(dir, "other.md"), "Only the second read should see this.")
	put(t, filepath.Join(dir, "release.md"), "Never include this body.")
	readOther := readNote("other.md")
	driver := aitest.New(aitest.Asks(readBuild), aitest.Asks(readOther), aitest.Asks(updateProposal))
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
		call       ai.ToolCall
		file, body string
	}{{readBuild, "build.md", "Use make."}, {readOther, "other.md", "Only the second read should see this."}} {
		messages := calls[i+1].Messages
		command, reply := messages[len(messages)-2], messages[len(messages)-1]
		if reply.Role != ai.RoleUser || command.Role != ai.RoleAssistant || !reflect.DeepEqual(command.Content.ToolCalls(), []ai.ToolCall{want.call}) {
			t.Fatalf("round %d malformed read exchange: %+v, %+v", i+2, command, reply)
		}
		results := reply.ToolResults()
		if len(results) != 1 || results[0].ToolCallID != want.call.ID || len(results[0].Content) != 2 ||
			!strings.Contains(results[0].Content[0].Text, fmt.Sprintf("Read rounds remaining: %d.", maxReadRounds-i-1)) ||
			!reflect.DeepEqual(results[0].Content[1], ai.TextBlock(want.file+"\n\n"+fmt.Sprintf("%6d\t%s\n", 1, want.body))) {
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
	m := Updater{Provider: fakeProvider{aitest.New(aitest.Asks(addProposal), aitest.Asks(readBuild), aitest.Asks(updateProposal), aitest.Asks(readBuild), aitest.Asks(forgetProposal))}}
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
		aitest.Asks(readBuild),
		aitest.Asks(updateProposal),
		aitest.Asks(ai.ToolCall{ID: "style", Name: tool.ToolWrite, Input: `{"file_path":"style.md","content":"---\nname: Style\ndescription: Writing preference\ntype: user\n---\nKeep examples concise."}`}),
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

func TestUpdateKeepsNoteAndIndexInSync(t *testing.T) {
	const old = "---\nname: Release nickname\ndescription: The release nickname is amber.\ntype: project\n---\n\nUse amber in release summaries.\n"
	const index = "- [Release nickname](build.md) — (project) The release nickname is amber.\n- [Other](other.md) — unrelated.\n"
	for _, tc := range []struct {
		name                       string
		edits                      []map[string]any
		invalidBatch, invalidPatch bool
	}{
		{name: "exact batch", edits: []map[string]any{
			{"old_string": "The release nickname is amber.", "new_string": "The release nickname is violet."},
			{"old_string": "Use amber in release summaries.", "new_string": "Use violet in release summaries."},
		}},
		{name: "replace all", edits: []map[string]any{{"old_string": "amber", "new_string": "violet", "replace_all": true}}},
		{name: "invalid second edit", invalidPatch: true, edits: []map[string]any{
			{"old_string": "Use amber", "new_string": "Use violet"},
			{"old_string": "text that does not exist", "new_string": "another value"},
		}},
		{name: "type change", invalidPatch: true, edits: []map[string]any{{"old_string": "type: project", "new_string": "type: reference"}}},
		{name: "second file", invalidBatch: true, edits: []map[string]any{
			{"old_string": "Use amber", "new_string": "Use violet"},
			{"file_path": "other.md", "old_string": "Other", "new_string": "Changed"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			notePath, indexPath := filepath.Join(dir, "build.md"), filepath.Join(dir, system.AutoMemoryIndexName)
			put(t, notePath, old)
			put(t, indexPath, index)
			var edits []ai.ToolCall
			for i, args := range tc.edits {
				if args["file_path"] == nil {
					args["file_path"] = "build.md"
				}
				edits = append(edits, ai.ToolCall{ID: fmt.Sprintf("edit-%d", i), Name: tool.ToolEdit, Input: requestJSON(t, args)})
			}
			m := Updater{Provider: fakeProvider{aitest.New(aitest.Asks(readBuild), aitest.Asks(edits...))}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Call the release violet instead of amber."))
			if tc.invalidBatch {
				if change != nil || !errors.Is(err, errInvalidProposal) {
					t.Fatalf("unexpected multiple-file result: change=%v err=%v", change, err)
				}
				return
			}
			if tc.invalidPatch && change == nil {
				if err == nil || get(t, notePath) != old || get(t, indexPath) != index {
					t.Fatalf("invalid staged edit changed live memory: %v", err)
				}
				return
			}
			if err != nil || change == nil || get(t, notePath) != old || get(t, indexPath) != index {
				t.Fatalf("expected an unapplied proposal, got change=%v err=%v", change, err)
			}
			_, err = change.Apply(t.Context())
			if tc.invalidPatch {
				if err == nil || get(t, notePath) != old || get(t, indexPath) != index {
					t.Fatalf("invalid edit partially applied: %v", err)
				}
				return
			}
			if err != nil || get(t, notePath) != strings.ReplaceAll(old, "amber", "violet") ||
				get(t, indexPath) != strings.ReplaceAll(index, "amber", "violet") {
				t.Fatalf("note and index diverged: %v", err)
			}
		})
	}
}

func TestUpdateBatchAndDirectIndex(t *testing.T) {
	const original = "- [Build](build.md) — Build command.\n"
	for _, tc := range []struct {
		name, replacement string
		wantError         bool
	}{
		{"rephrase", "- [Build method](build.md) — How to compile.\n", false},
		{"remove link", "# No notes\n", true},
		{"add untracked link", original + "- [Unknown](unknown.md)\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			index := filepath.Join(dir, system.AutoMemoryIndexName)
			note := filepath.Join(dir, "build.md")
			put(t, index, original)
			put(t, note, "Use make.")
			indexEdit := ai.ToolCall{ID: "index", Name: tool.ToolEdit, Input: requestJSON(t, map[string]string{
				"file_path": system.AutoMemoryIndexName, "old_string": original, "new_string": tc.replacement,
			})}
			add := ai.ToolCall{ID: "add", Name: tool.ToolWrite, Input: requestJSON(t, map[string]string{
				"file_path": "demo.md", "content": "---\nname: Demo environment\ndescription: Demo conventions\ntype: project\n---\nCall it Violet.\n",
			})}
			driver := aitest.New(aitest.Asks(readNote(system.AutoMemoryIndexName), readBuild), aitest.Asks(add, indexEdit, updateProposal))
			m := Updater{Provider: fakeProvider{driver}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use go build; call the demo Violet; clarify the index."))
			if err != nil || change == nil || get(t, index) != original || get(t, note) != "Use make." {
				t.Fatalf("expected a staged batch: change=%v err=%v", change, err)
			}
			notice, err := change.Apply(t.Context())
			added := filepath.Join(dir, "demo.md")
			if tc.wantError {
				if _, statErr := os.Stat(added); err == nil || !errors.Is(statErr, fs.ErrNotExist) || get(t, index) != original || get(t, note) != "Use make." {
					t.Fatalf("bad index edit changed live notes: %v, %v", err, statErr)
				}
				return
			}
			if err != nil || notice != "Memory · updated MEMORY.md, saved demo.md, updated build.md" ||
				!strings.HasPrefix(get(t, index), tc.replacement) || !strings.Contains(get(t, index), "(demo.md)") ||
				!strings.Contains(get(t, added), "Violet") || get(t, note) != "Use go build." {
				t.Fatalf("unexpected batch: notice=%q err=%v", notice, err)
			}
		})
	}
}

func TestProposeRejectsInvalidAndUncataloguedOperations(t *testing.T) {
	for _, tc := range []struct {
		name       string
		call       ai.ToolCall
		catalogued bool
	}{
		{"unexpected Write argument", ai.ToolCall{Name: tool.ToolWrite, Input: strings.Replace(addProposal.Input, `"file_path":`, `"scope":"project","file_path":`, 1)}, false},
		{"unknown note type", ai.ToolCall{Name: tool.ToolWrite, Input: strings.Replace(addProposal.Input, `type: project`, `type: unknown`, 1)}, false},
		{"note filename in description", ai.ToolCall{Name: tool.ToolWrite, Input: strings.Replace(addProposal.Input, `Build command`, `Replaces deploy.md`, 1)}, false},
		{"uncatalogued update", updateProposal, false},
		{"uncatalogued forget", forgetProposal, false},
		{"unread update", updateProposal, true},
		{"unread forget", forgetProposal, true},
		{"read with offset", ai.ToolCall{ID: "read", Name: tool.ToolRead, Input: `{"file_path":"build.md","offset":1}`}, true},
		{"read without targets", ai.ToolCall{ID: "read", Name: tool.ToolRead, Input: `{}`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := system.MemoryStore(dir)
			note := filepath.Join(dir, "build.md")
			put(t, note, "Use make.")
			if tc.catalogued {
				put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n")
			}
			m := Updater{Provider: fakeProvider{aitest.New(aitest.Asks(tc.call))}}
			change, err := m.Propose(t.Context(), store, completedPrefix("Discuss the build command."))
			if change != nil || !errors.Is(err, errInvalidProposal) || get(t, note) != "Use make." {
				t.Fatalf("change=%v err=%v", change, err)
			}
		})
	}
}

func TestProposeDoesNotFallBackToJSONForMissingTool(t *testing.T) {
	prefix := completedPrefix("Use make for builds.")
	prefix.Tools = nil
	for _, turn := range []aitest.Turn{
		aitest.Asks(addProposal),
		aitest.Says(`{"action":"remember","file":"build.md","name":"Build","description":"Build command","type":"project","body":"Use make."}`),
	} {
		driver := aitest.New(turn)
		m := Updater{Provider: fakeProvider{driver}}
		change, err := m.Propose(t.Context(), system.MemoryStore(t.TempDir()), prefix)
		if change != nil || !errors.Is(err, errInvalidProposal) || strings.Contains(driver.Last().Messages[2].Text(), "use Write") {
			t.Fatalf("expected an unavailable tool to stay unavailable: change=%v err=%v", change, err)
		}
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
	for _, call := range []ai.ToolCall{addProposal, updateProposal, forgetProposal} {
		call.Input = strings.Replace(call.Input, "build.md", system.InstructionFile, 1)
		driver := aitest.New(aitest.Asks(call))
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
	driver := aitest.New(aitest.Asks(addProposal))
	m := Updater{Provider: fakeProvider{driver}}
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use make."))
	if change != nil || !errors.Is(err, errInvalidProposal) {
		t.Fatalf("incomplete index allowed add: change=%v err=%v", change, err)
	}
	blocks := driver.Last().Messages[2].Content
	if len(blocks) != 5 || blocks[3].Text != visible || !strings.Contains(blocks[0].Text, "search the omitted index") ||
		!strings.Contains(blocks[4].Text, "…") {
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
			driver := aitest.New(aitest.Says(tc.read), aitest.Asks(readBuild), aitest.Asks(updateProposal))
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
			if len(blocks) != 5 || blocks[3].Text != tc.prefix || !strings.Contains(blocks[4].Text, tc.read) || !strings.Contains(blocks[4].Text, "…") {
				t.Fatal("initial Update preview did not preserve the raw prefix and separate the hint")
			}
			if !strings.Contains(calls[1].Messages[4].Text(), tail) {
				t.Fatal("Update lookup did not return the omitted entry")
			}
			t.Logf("Update Aside · %s cutoff · appended user message\n%s\n%s\n[raw preview body omitted from terminal sample]%s", tc.name, strings.SplitN(blocks[0].Text, "\n", 2)[0], strings.SplitN(blocks[1].Text, "\n", 2)[0], blocks[4].Text)
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
		m := Updater{Provider: fakeProvider{aitest.New(aitest.Says(noMemoryChanges))}}
		change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use make."))
		if change != nil || err == nil || !strings.Contains(err.Error(), tc.problem) {
			t.Fatalf("index %.40q: change=%v err=%v; want %q", tc.index, change, err, tc.problem)
		}
	}
}

func TestProposeStopsOnReadLimit(t *testing.T) {
	dir := t.TempDir()
	var index strings.Builder
	var turns []aitest.Turn
	for i := 0; i <= maxReadRounds; i++ {
		file := fmt.Sprintf("topic%d.md", i)
		fmt.Fprintf(&index, "- [Topic](%s)\n", file)
		put(t, filepath.Join(dir, file), "Original text.")
		turns = append(turns, aitest.Asks(readNote(file)))
	}
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), index.String())
	driver := aitest.New(turns...)
	m := Updater{Provider: fakeProvider{driver}}
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("Use go build instead."))
	if change != nil || err == nil || driver.Calls() != maxReadRounds+1 {
		t.Fatalf("read limit did not stop the update: calls=%d change=%v err=%v", driver.Calls(), change, err)
	}
}

func TestProposeRejectsStaleSnapshot(t *testing.T) {
	for _, edited := range []string{"build.md", "other.md", system.AutoMemoryIndexName} {
		t.Run(edited, func(t *testing.T) {
			dir := t.TempDir()
			put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Other](other.md)\n- [Build](build.md)\n")
			put(t, filepath.Join(dir, "other.md"), "Other evidence.")
			put(t, filepath.Join(dir, "build.md"), "Use make.")
			m := Updater{Provider: fakeProvider{aitest.New(aitest.Asks(readBuild, readNote("other.md")), aitest.Asks(updateProposal))}}
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
