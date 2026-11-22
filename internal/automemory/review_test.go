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

func reviewPrefix(text string) *ai.Request {
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

func TestReviewContext(t *testing.T) {
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
	m := Maintainer{Provider: fakeProvider{driver}, Model: "maintain-model"}
	change, err := m.Review(t.Context(), store, prefix, Actions{Add: true, Forget: true})
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
	payload, ok := strings.CutPrefix(suffix.Content.Text(), reviewPrompt+"\n\n")
	if suffix.Role != ai.RoleUser || !ok || len(suffix.Content) != 1 {
		t.Fatalf("unexpected proposal suffix: %+v", suffix)
	}
	var got struct {
		Enabled []memoryAction
		Index   string
		Notes   []noteEvidence
		Types   map[string]string `json:"note_types"`
	}
	if err := core.DecodeStrictJSON(strings.NewReader(payload), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Types, noteTypes) || !reflect.DeepEqual(got.Enabled, []memoryAction{actionRemember, actionForget}) || got.Index != index ||
		!reflect.DeepEqual(got.Notes, []noteEvidence{{"deploy.md", "Deploy only from release branches.\n"}}) {
		t.Fatalf("unexpected review payload: %+v", got)
	}
	if requestJSON(t, prefix) != before || !reflect.DeepEqual(prefix.Messages[:cap(prefix.Messages)][len(history)], ai.Message{}) {
		t.Fatal("review mutated native prefix or its spare message capacity")
	}
}

const (
	reviewAdd    = `{"action":"remember","file":"build.md","name":"Build","description":"Build command","type":"project","body":"Use make."}`
	reviewUpdate = `{"action":"update","file":"build.md","old":"Use make.","replacement":"Use go build."}`
	reviewForget = `{"action":"forget","file":"build.md"}`
)

func TestReviewLifecycle(t *testing.T) {
	dir := t.TempDir()
	store := system.MemoryStore(dir)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	projectNote := filepath.Join(dir, "build.md")
	put(t, index, "- [Deploy](deploy.md)\n")
	put(t, filepath.Join(dir, "deploy.md"), "Keep this note.")
	m := Maintainer{Provider: fakeProvider{aitest.New(aitest.Says(reviewAdd), aitest.Says(reviewUpdate), aitest.Says(reviewForget))}}
	for i, tc := range []struct {
		user    string
		actions Actions
	}{
		{"This project builds with make.", Actions{Add: true}},
		{"We now use go build, not make.", Actions{Update: true}},
		{"Please remove the stored build note; it no longer applies.", Actions{Forget: true}},
	} {
		before := get(t, index)
		change, err := m.Review(t.Context(), store, reviewPrefix(tc.user), tc.actions)
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

func TestReviewNoteTypesAreMetadata(t *testing.T) {
	dir := t.TempDir()
	store := system.MemoryStore(dir)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	build := filepath.Join(dir, "build.md")
	const link = "- [Build](build.md) — build command\n"
	put(t, index, link)
	put(t, build, "---\nname: Build\ntype: feedback\n---\nUse make.")
	m := Maintainer{Provider: fakeProvider{aitest.New(
		aitest.Says(reviewUpdate),
		aitest.Says(`{"action":"remember","file":"style.md","name":"Style","description":"Writing preference","type":"user","body":"Keep examples concise."}`),
	)}}
	for _, actions := range []Actions{{Update: true}, {Add: true}} {
		change, err := m.Review(t.Context(), store, reviewPrefix("For this project I prefer concise examples and go build."), actions)
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

func TestReviewRejectsDisabledAndUncataloguedOperations(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		actions        Actions
		catalogued     bool
	}{
		{"disabled add", reviewAdd, Actions{Update: true}, false},
		{"legacy scope field", strings.Replace(reviewAdd, `"file":`, `"scope":"project","file":`, 1), Actions{Add: true}, false},
		{"unknown note type", strings.Replace(reviewAdd, `"type":"project"`, `"type":"unknown"`, 1), Actions{Add: true}, false},
		{"disabled update", reviewUpdate, Actions{Forget: true}, true},
		{"disabled forget", reviewForget, Actions{Add: true}, true},
		{"uncatalogued update", reviewUpdate, Actions{Update: true}, false},
		{"uncatalogued forget", reviewForget, Actions{Forget: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := system.MemoryStore(dir)
			note := filepath.Join(dir, "build.md")
			put(t, note, "Use make.")
			if tc.catalogued {
				put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n")
			}
			m := Maintainer{Provider: fakeProvider{aitest.New(aitest.Says(tc.response))}}
			change, err := m.Review(t.Context(), store, reviewPrefix("Discuss the build command."), tc.actions)
			if change != nil || !errors.Is(err, errInvalidProposal) || get(t, note) != "Use make." {
				t.Fatalf("change=%v err=%v", change, err)
			}
		})
	}
}

func TestReviewRefusesInstructionFileEvenIfIndexed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, system.InstructionFile)
	const instructions = "Use make."
	put(t, path, instructions)
	index := filepath.Join(dir, system.AutoMemoryIndexName)
	const link = "- [Instructions](AGENTS.md)\n"
	put(t, index, link)
	for _, response := range []string{reviewAdd, reviewUpdate, reviewForget} {
		driver := aitest.New(aitest.Says(strings.Replace(response, "build.md", system.InstructionFile, 1)))
		m := Maintainer{Provider: fakeProvider{driver}}
		change, err := m.Review(t.Context(), system.MemoryStore(dir), reviewPrefix("Update the project instructions."), Actions{Add: true, Update: true, Forget: true})
		if change != nil || !errors.Is(err, errInvalidProposal) || get(t, path) != instructions || get(t, index) != link {
			t.Fatalf("instruction file must not be an Auto Memory target: change=%v, err=%v", change, err)
		}
	}
}

func TestReviewSkipsWithoutOptInStorePrefixOrUserEvidence(t *testing.T) {
	project := system.MemoryStore(t.TempDir())
	user := reviewPrefix("Use go build.")
	for _, tc := range []struct {
		store   system.MemoryStore
		prefix  *ai.Request
		actions Actions
	}{
		{project, user, Actions{}},
		{"", user, Actions{Add: true}},
		{project, nil, Actions{Add: true}},
		{project, reviewPrefix("<system-reminder>Not user evidence.</system-reminder>"), Actions{Add: true}},
		{project, &ai.Request{Messages: []ai.Message{core.AssistantMessage("Assistant-only claim.", "", nil)}}, Actions{Add: true}},
	} {
		if change, err := (Maintainer{}).Review(t.Context(), tc.store, tc.prefix, tc.actions); change != nil || err != nil {
			t.Fatalf("change=%v err=%v", change, err)
		}
	}
}

func TestReviewPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	change, err := (Maintainer{}).Review(ctx, system.MemoryStore(t.TempDir()), reviewPrefix("Use go build."), Actions{Add: true})
	if change != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("change=%v err=%v", change, err)
	}
}

func TestReviewRejectsStaleSnapshot(t *testing.T) {
	for _, edited := range []string{"build.md", "other.md", system.AutoMemoryIndexName} {
		t.Run(edited, func(t *testing.T) {
			dir := t.TempDir()
			put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Other](other.md)\n- [Build](build.md)\n")
			put(t, filepath.Join(dir, "other.md"), "Other evidence.")
			put(t, filepath.Join(dir, "build.md"), "Use make.")
			m := Maintainer{Provider: fakeProvider{aitest.New(aitest.Says(reviewUpdate))}}
			change, err := m.Review(t.Context(), system.MemoryStore(dir), reviewPrefix("Use go build instead."), Actions{Update: true})
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
