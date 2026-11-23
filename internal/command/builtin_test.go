package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReflectionAndMemoryCatalog(t *testing.T) {
	reg := &Registry{}
	catalog := reg.BuiltinNames()
	reflect, ok := catalog["reflect"]
	if !ok || !strings.Contains(reflect.Description, "Auto Memory") || !strings.Contains(reflect.Description, "Auto Skill") {
		t.Fatalf("/reflect should advertise both settings: %+v", reflect)
	}
	if _, ok := catalog["reflection"]; ok {
		t.Fatal("/reflection is a hidden panel alias")
	}
	if matches := reg.Matching("reflect "); len(matches) != 1 || matches[0].Name != "reflect memory" {
		t.Fatalf("/reflect should suggest the memory action: %+v", matches)
	}
	if matches := reg.Matching("memory u"); len(matches) != 0 {
		t.Fatalf("/memory should no longer suggest update: %+v", matches)
	}
	if memory, ok := catalog["memory"]; !ok || !strings.Contains(memory.Description, "repository memory") || strings.Contains(memory.Description, "Auto Skill") {
		t.Fatalf("/memory should advertise the browser: %+v", memory)
	}
	if _, ok := catalog["evolve"]; ok {
		t.Fatal("/evolve is still in the built-in command catalog")
	}
}

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
	instructions := pc.Instructions()
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
	if got := pc.Instructions(); !strings.Contains(got, "Do it my way.") {
		t.Fatalf("instructions should come from the project file, got %q", got)
	}
}
