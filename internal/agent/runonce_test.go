package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// Headless runs use the same project instruction framing as interactive runs.
func TestAHeadlessRunCarriesTheProjectsInstructions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("always answer PINEAPPLE"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := withInstructions(dir, core.UserMessage("what is the word?", nil)).Text()
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

func TestNothingToSayAddsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	msg := core.UserMessage("hello", nil)
	out := withInstructions(t.TempDir(), msg)
	if out.Text() != "hello" {
		t.Errorf("an empty project injected %q", out.Text())
	}
}
