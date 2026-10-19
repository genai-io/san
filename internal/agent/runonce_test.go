package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
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
	if !strings.Contains(out, `<memory scope="project">`) {
		t.Errorf("not wrapped as project memory: %q", out)
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
	empty := withInstructions(t.TempDir(), system.MemoryStores{User: t.TempDir()}, core.UserMessage("hi", nil)).Text()
	if !strings.Contains(empty, "(no memories yet)") {
		t.Errorf("an empty store should be named as empty: %q", empty)
	}
	if !strings.Contains(out, "go.md") || !strings.Contains(out, "needs fixing") {
		t.Errorf("dangling link not reported: %q", out)
	}
}
