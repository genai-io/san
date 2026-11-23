package automemory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestUpdateForgetMemory(t *testing.T) {
	dir := t.TempDir()
	index, note := filepath.Join(dir, system.AutoMemoryIndexName), filepath.Join(dir, "build.md")
	keepNote := filepath.Join(dir, "keep.md")
	const keep = "- [Keep](keep.md)\n"
	const originalIndex = "- [Build](build.md)\n" + keep
	put(t, index, originalIndex)
	put(t, note, "Use make.")
	put(t, keepNote, "Keep this note.")
	call := ai.ToolCall{ID: "forget", Name: "ForgetMemory", Input: `{"file":"build.md"}`}
	driver := aitest.New(aitest.Asks(readBuild), aitest.Asks(call))
	prefix := completedPrefix("Please forget the stored build note.")
	prefix.ToolChoice = ai.ToolChoiceAuto
	// Give the prefix spare capacity to catch appending to its backing array.
	tools := make([]ai.Schema, len(prefix.Tools)+1)
	copy(tools, prefix.Tools)
	prefix.Tools = tools[:len(tools)-1]
	originalTools := slices.Clone(tools)
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "forget"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	m := Updater{Provider: fakeProvider{driver}, Recording: owner}
	change, err := m.Propose(t.Context(), system.MemoryStore(dir), prefix)
	if err != nil || change == nil || get(t, note) != "Use make." || get(t, index) != originalIndex {
		t.Fatalf("expected a deferred change, got %v, %v", change, err)
	}
	t.Cleanup(func() { _ = change.Discard() })
	if !reflect.DeepEqual(tools, originalTools) || !reflect.DeepEqual(prefix.Tools, originalTools[:len(tools)-1]) || prefix.ToolChoice != ai.ToolChoiceAuto {
		t.Fatal("modified the main request's tools or choice")
	}
	if driver.Calls() != 2 {
		t.Fatalf("got %d model calls, want read and forget", driver.Calls())
	}
	for _, req := range driver.Sent() {
		if len(req.Tools) != len(tools) || !reflect.DeepEqual(req.Tools[:len(tools)-1], originalTools[:len(tools)-1]) ||
			req.Tools[len(tools)-1].Name != call.Name || req.ToolChoice != ai.ToolChoiceAuto {
			t.Fatalf("expected scoped ForgetMemory after existing tools with Auto choice: %+v", req)
		}
	}
	if notice, err := change.Apply(t.Context()); err != nil || notice != "Memory · forgot build.md" {
		t.Fatalf("apply = %q, %v", notice, err)
	}
	if _, err := os.Stat(note); !errors.Is(err, fs.ErrNotExist) || get(t, index) != keep || get(t, keepNote) != "Keep this note." {
		t.Fatalf("expected only the build note and its link removed; build note stat: %v", err)
	}
	var recordedCall, recordedNotice bool
	snapshot := owner.Snapshot()
	for _, e := range snapshot.Events() {
		switch p := e.Payload.(type) {
		case *session.InferenceFinishPayload:
			recordedCall = recordedCall || p.StopReason == string(ai.StopToolUse)
		case *session.AnnotationAddPayload:
			recordedNotice = recordedNotice || (p.Text == "Memory · forgot build.md" && e.Source != nil)
		}
	}
	if !recordedCall || !recordedNotice {
		t.Fatal("missing raw response or source-linked deletion notice")
	}
}

func TestUpdateForgetMemoryNotOfferedWithoutRead(t *testing.T) {
	prefix := completedPrefix("Please forget the stored build note.")
	prefix.Tools = []ai.Schema{{Name: tool.ToolBash}, {Name: tool.ToolEdit}}
	prefix.ToolChoice = ai.ToolChoiceAuto
	driver := aitest.New(aitest.Says(noMemoryChanges))
	m := Updater{Provider: fakeProvider{driver}}
	change, err := m.Propose(t.Context(), system.MemoryStore(t.TempDir()), prefix)
	if err != nil || change != nil {
		t.Fatalf("expected no change, got %v, %v", change, err)
	}
	if req := driver.Last(); req == nil || !reflect.DeepEqual(req.Tools, prefix.Tools) {
		t.Fatalf("expected no added tool without Read: %+v", req)
	}
}

func TestForgetMemoryRejectsInvalidProposals(t *testing.T) {
	const valid = `{"file":"build.md"}`
	for _, tc := range []struct {
		name, toolName, input string
		skipRead, multiple    bool
	}{
		{name: "unread", input: valid, skipRead: true},
		{name: "out of store", input: `{"file":"../build.md"}`},
		{name: "directory component", input: `{"file":"./build.md"}`},
		{name: "absolute path", input: `{"file":"/tmp/build.md"}`},
		{name: "index", input: `{"file":"MEMORY.md"}`},
		{name: "unknown argument", input: `{"file":"build.md","command":"rm build.md"}`},
		{name: "missing argument", input: `{}`},
		{name: "non-string argument", input: `{"file":["build.md"]}`},
		{name: "trailing JSON", input: valid + `{}`},
		{name: "bash listed in prefix", toolName: tool.ToolBash, input: `{"command":"rm build.md"}`},
		{name: "powershell listed in prefix", toolName: tool.ToolPowerShell, input: `{"command":"Remove-Item -LiteralPath build.md"}`},
		{name: "multiple writes", input: valid, multiple: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			index, note := filepath.Join(dir, system.AutoMemoryIndexName), filepath.Join(dir, "build.md")
			const link = "- [Build](build.md)\n"
			put(t, index, link)
			put(t, note, "Use make.")
			name := tc.toolName
			if name == "" {
				name = toolForgetMemory
			}
			calls := []ai.ToolCall{{ID: "forget", Name: name, Input: tc.input}}
			if tc.multiple {
				calls = append(calls, ai.ToolCall{ID: "second", Name: toolForgetMemory, Input: valid})
			}
			var turns []aitest.Turn
			if !tc.skipRead {
				turns = append(turns, aitest.Asks(readBuild))
			}
			turns = append(turns, aitest.Asks(calls...))
			prefix := completedPrefix("Please forget the stored build note.")
			prefix.Tools = []ai.Schema{{Name: tool.ToolRead}, {Name: tool.ToolBash}, {Name: tool.ToolPowerShell}}
			m := Updater{Provider: fakeProvider{aitest.New(turns...)}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), prefix)
			if change != nil || !errors.Is(err, errInvalidProposal) || get(t, note) != "Use make." || get(t, index) != link {
				t.Fatalf("expected rejection without writes, got %v, %v", change, err)
			}
		})
	}
}
