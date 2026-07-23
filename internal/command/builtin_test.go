package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimplifyShipsAsBuiltinPromptCommand(t *testing.T) {
	reg := &Registry{cwd: t.TempDir()}

	pc, ok := reg.IsCustomCommand("simplify")
	if !ok {
		t.Fatal("/simplify should resolve as a builtin prompt command")
	}
	if pc.Scope != scopeBuiltin || pc.FilePath != "" {
		t.Fatalf("builtin command metadata = %+v", pc)
	}
	if pc.Description == "" {
		t.Fatal("builtin command needs a description for the /help listing")
	}
	instructions := pc.GetInstructions()
	for _, want := range []string{"Phase 0", "Reuse", "Simplification", "Efficiency", "Altitude", "Agent tool"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("simplify workflow should mention %q, got %d bytes", want, len(instructions))
		}
	}

	// The builtin also surfaces in the aggregate listing for autocomplete.
	found := false
	for _, info := range reg.List() {
		if info.Name == "simplify" {
			found = true
		}
	}
	if !found {
		t.Fatal("/simplify should appear in the command listing")
	}
}

func TestDiskCommandShadowsBuiltinPromptCommand(t *testing.T) {
	root := t.TempDir()
	cmdsDir := filepath.Join(root, ".san", "commands")
	if err := os.MkdirAll(cmdsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "---\nname: simplify\ndescription: my own pass\n---\nDo it my way.\n"
	if err := os.WriteFile(filepath.Join(cmdsDir, "simplify.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := &Registry{cwd: root}
	pc, ok := reg.IsCustomCommand("simplify")
	if !ok {
		t.Fatal("/simplify should still resolve")
	}
	if pc.FilePath == "" || pc.Scope == scopeBuiltin {
		t.Fatalf("a project command must shadow the builtin, got %+v", pc)
	}
	if got := pc.GetInstructions(); !strings.Contains(got, "Do it my way.") {
		t.Fatalf("instructions should come from the project file, got %q", got)
	}
}
