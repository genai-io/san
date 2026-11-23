package automemory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestUpdateReusesShellForForget(t *testing.T) {
	for _, tc := range []struct{ shell, command string }{
		{tool.ToolBash, "rm 'build.md'"},
		{tool.ToolPowerShell, "Remove-Item -LiteralPath 'build.md'"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			dir := t.TempDir()
			index, note := filepath.Join(dir, system.AutoMemoryIndexName), filepath.Join(dir, "build.md")
			const keep = "- [Keep](keep.md)\n"
			put(t, index, "- [Build](build.md)\n"+keep)
			put(t, note, "Use make.")
			call := ai.ToolCall{ID: "forget", Name: tc.shell, Input: requestJSON(t, map[string]string{"command": tc.command})}
			driver := aitest.New(aitest.Says(readBuild), aitest.Asks(call))
			prefix := completedPrefix("Please forget the stored build note.")
			prefix.Tools = []ai.Schema{{Name: "Read"}, {Name: tc.shell}, {Name: "Edit"}}
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
			if err != nil || change == nil || get(t, note) != "Use make." || get(t, index) != "- [Build](build.md)\n"+keep {
				t.Fatalf("expected a deferred change, got %v, %v", change, err)
			}
			for _, req := range driver.Sent() {
				if !reflect.DeepEqual(req.Tools, prefix.Tools) || req.ToolChoice != prefix.ToolChoice {
					t.Fatalf("changed original tool schemas or choice: %+v", req)
				}
			}
			if notice, err := change.Apply(t.Context()); err != nil || notice != "Memory · forgot build.md" {
				t.Fatalf("apply = %q, %v", notice, err)
			}
			if _, err := os.Stat(note); !errors.Is(err, fs.ErrNotExist) || get(t, index) != keep {
				t.Fatalf("forget failed to remove both the note and its link: %v", err)
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
		})
	}
}

func TestForgetRejectsUnsupportedShellCalls(t *testing.T) {
	for _, tc := range []struct{ name, shell, command string }{
		{"unavailable", tool.ToolPowerShell, "Remove-Item -LiteralPath build.md"},
		{"unread", tool.ToolBash, "rm build.md"},
		{"path", tool.ToolBash, "rm ../build.md"},
		{"recursive", tool.ToolBash, "rm -r build.md"},
		{"multiple targets", tool.ToolBash, "rm build.md other.md"},
		{"multiple commands", tool.ToolBash, "rm build.md; echo no"},
		{"index", tool.ToolBash, "rm MEMORY.md"},
		{"wildcard", tool.ToolPowerShell, "Remove-Item -LiteralPath '*.md'"},
		{"additional PowerShell syntax", tool.ToolPowerShell, "Remove-Item -LiteralPath build.md; Write-Output no"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			index, note := filepath.Join(dir, system.AutoMemoryIndexName), filepath.Join(dir, "build.md")
			const link = "- [Build](build.md)\n"
			put(t, index, link)
			put(t, note, "Use make.")
			call := ai.ToolCall{ID: "forget", Name: tc.shell, Input: requestJSON(t, map[string]string{"command": tc.command})}
			turns := []aitest.Turn{aitest.Asks(call)}
			if tc.name != "unread" {
				turns = append([]aitest.Turn{aitest.Says(readBuild)}, turns...)
			}
			prefix := completedPrefix("Please forget the stored build note.")
			shell := tc.shell
			if tc.name == "unavailable" {
				shell = tool.ToolBash
			}
			prefix.Tools = []ai.Schema{{Name: shell}}
			m := Updater{Provider: fakeProvider{aitest.New(turns...)}}
			change, err := m.Propose(t.Context(), system.MemoryStore(dir), prefix)
			if change != nil || !errors.Is(err, errInvalidProposal) || get(t, note) != "Use make." || get(t, index) != link {
				t.Fatalf("expected rejection without writes, got %v, %v", change, err)
			}
		})
	}
}
