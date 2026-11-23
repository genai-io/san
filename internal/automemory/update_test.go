package automemory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type fakeProvider struct{ driver *aitest.Driver }

func (p fakeProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}
func (fakeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (fakeProvider) Name() string                                        { return "fake" }

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProposeRefusesLiteralEOFWithoutWrites(t *testing.T) {
	for _, index := range []string{"# Memory\r\n\r\n```markdown", "~~~\nexample\n", "<!--\nexample\n", "<div>\nexample\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, system.AutoMemoryIndexName)
		put(t, path, index)
		m := Updater{Provider: fakeProvider{aitest.New(aitest.Asks(addProposal))}}
		change, err := m.Propose(t.Context(), system.MemoryStore(dir), completedPrefix("We use make for all builds in this repository."))
		if err != nil || change == nil {
			t.Fatalf("change=%v err=%v", change, err)
		}
		notice, err := change.Apply(t.Context())
		if notice != "" || err == nil || !strings.Contains(err.Error(), "Markdown literal block") || get(t, path) != index {
			t.Fatalf("index %q: notice=%q err=%v; want refusal and unchanged index", index, notice, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "build.md")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("unexpected note stat result: %v", err)
		}
	}
}

func TestProposeRejectsSymlinkNoteAndToolProposal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	put(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "build.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	put(t, filepath.Join(dir, system.AutoMemoryIndexName), "- [Build](build.md)\n")
	for _, turn := range []aitest.Turn{aitest.Asks(readBuild), aitest.Asks(ai.ToolCall{ID: "1", Name: "Remove", Input: `{}`})} {
		driver := aitest.New(turn)
		m := Updater{Provider: fakeProvider{driver}}
		prefix := completedPrefix("Please forget the stored build note.")
		prefix.Tools = append(prefix.Tools, ai.Schema{Name: "Remove", Definition: map[string]any{"type": "object"}})
		change, err := m.Propose(t.Context(), system.MemoryStore(dir), prefix)
		if change != nil || err == nil || get(t, outside) != "outside" {
			t.Fatalf("untrusted target: change=%v err=%v", change, err)
		}
		// Auto is kept so the main request's cached messages stay valid.
		if driver.Calls() != 1 || driver.Last().ToolChoice != ai.ToolChoiceAuto {
			t.Fatalf("expected one cache-preserving proposal call, got %d calls", driver.Calls())
		}
	}
}
