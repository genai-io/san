package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
)

// A headless run reads the project's instructions, because a repo that answers
// one way interactively and another way through -p is a repo with two
// behaviours. They ride on the opening message as a memory reminder, the same
// shape the interactive path uses.
func TestAHeadlessRunCarriesTheProjectsInstructions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("always answer PINEAPPLE"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := withInstructions(dir, system.MemoryStores{}, core.UserMessage("what is the word?", nil)).Text()
	if !strings.Contains(out, "always answer PINEAPPLE") {
		t.Errorf("the instructions did not reach the message: %q", out)
	}
	if !strings.Contains(out, `source="memory-project"`) || !strings.Contains(out, "(project instructions):") || strings.Contains(out, `<memory scope="project">`) {
		t.Errorf("wrong framing for project instructions: %q", out)
	}
	if !strings.HasPrefix(out, "what is the word?") {
		t.Errorf("the person's message stopped coming first: %q", out)
	}
}

// A repo with nothing to say adds nothing — no empty scope, no bare wrapper.
func TestNothingToSayAddsNothing(t *testing.T) {
	msg := core.UserMessage("hello", nil)
	out := withInstructions(t.TempDir(), system.MemoryStores{}, msg)
	if out.Text() != "hello" {
		t.Errorf("an empty project injected %q", out.Text())
	}
}

// A headless run sees the agent's memory index too, as the interactive one does.
func TestAHeadlessRunCarriesTheMemoryIndex(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := t.TempDir()
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Go](go.md) — prefers table tests"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := t.TempDir()
	if err := os.WriteFile(filepath.Join(user, "MEMORY.md"), []byte("- terse replies"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := withInstructions(t.TempDir(), system.MemoryStores{User: user, Project: mem}, core.UserMessage("hi", nil)).Text()
	if !strings.Contains(out, `<memory scope="auto">`) || !strings.Contains(out, "prefers table tests") || !strings.Contains(out, "terse replies") {
		t.Errorf("a store's index is missing: %q", out)
	}
	if !strings.Contains(out, "go.md") || !strings.Contains(out, "needs fixing") {
		t.Errorf("dangling link not reported: %q", out)
	}
}

func TestAutoMemoryChangesOnlyTheReminder(t *testing.T) {
	params := BuildParams{CWD: t.TempDir()}
	base := params.System().Prompt()
	updates := reminder.New()
	updates.Register(reminder.ProviderMemoryAuto, func() string { return AutoMemoryReminder(params.AutoMemory) })

	if got := updates.PrepareInput(core.UserMessage("off", nil)).Text(); got != "off" {
		t.Fatalf("disabled auto memory added context: %q", got)
	}
	params.AutoMemory = system.MemoryStores{User: filepath.Join(t.TempDir(), "memory"), Project: t.TempDir()}
	updates.RequeueSystemReminders()
	got := updates.PrepareInput(core.UserMessage("on", nil)).Text()
	if !strings.Contains(got, `source="memory-auto"`) || !strings.Contains(got, system.AutoMemoryGuide()) ||
		!strings.Contains(got, params.AutoMemory.User) || !strings.Contains(got, params.AutoMemory.Project) {
		t.Fatalf("missing guide or empty store locations: %q", got)
	}
	if strings.Contains(got, `<memory scope="auto">`) {
		t.Fatalf("an empty index was injected: %q", got)
	}
	if got := params.System().Prompt(); got != base {
		t.Fatal("enabled auto memory changed the system prompt")
	}
	updates.RequeueSystemReminders()
	if got := updates.PrepareInput(core.UserMessage("unchanged", nil)).Text(); got != "unchanged" {
		t.Fatalf("unchanged auto memory was injected again: %q", got)
	}
	params.AutoMemory = system.MemoryStores{}
	updates.RequeueSystemReminders()
	if got := updates.PrepareInput(core.UserMessage("off again", nil)).Text(); !strings.Contains(got, `source="memory-auto"`) ||
		!strings.Contains(got, "no longer apply") || strings.Contains(got, system.AutoMemoryGuide()) {
		t.Fatalf("disabling auto memory should send only a withdrawal: %q", got)
	}
}

func TestAutoMemoryReminderBoundsIndexPreview(t *testing.T) {
	dir := t.TempDir()
	index := "- first topic\n" + strings.Repeat("- "+strings.Repeat("索引", 40)+"\n", 20) + "- last topic"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	got := AutoMemoryReminder(system.MemoryStores{Project: dir})
	_, preview, ok := strings.Cut(got, "<memory scope=\"auto\">\nProject store MEMORY.md:\n")
	preview, _, closed := strings.Cut(preview, "\n</memory>")
	index, _, truncated := strings.Cut(preview, "\nRead MEMORY.md")
	if !ok || !closed || !truncated || len(index) > 2*1024 || !strings.Contains(index, "first topic") || strings.Contains(index, "last topic") {
		t.Fatalf("index preview has the wrong contents or size: %d bytes", len(index))
	}
}
