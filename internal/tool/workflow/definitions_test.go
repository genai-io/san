package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const editable = "---\nname: review\n---\n```mermaid\nflowchart LR\n  work\n```\n\n## work\nmode: explore\n\nReview {{input.task}}"

func TestDefinitionSaveRequiresValidReviewedSource(t *testing.T) {
	dir := t.TempDir()
	wt := NewWorkflowTool()
	wt.SetSearchPaths([]string{dir})
	if err := wt.SaveDefinition("review", editable, ""); err != nil {
		t.Fatal(err)
	}
	got, err := wt.Definition("review")
	if err != nil || got != editable {
		t.Fatalf("source = %q, err = %v", got, err)
	}
	if err := wt.SaveDefinition("review", editable, ""); err == nil {
		t.Fatal("new draft overwrote an existing workflow")
	}
	changed := strings.Replace(editable, "Review {{input.task}}", "Check {{input.task}}", 1)
	if err := wt.SaveDefinition("review", changed, editable); err != nil {
		t.Fatal(err)
	}
	if err := wt.SaveDefinition("review", editable, editable); err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("stale editor save: %v", err)
	}
	if got, _ := wt.Definition("review"); got != changed {
		t.Fatal("stale edit changed the saved definition")
	}
	if err := wt.SaveDefinition("review", "not a workflow", changed); err == nil {
		t.Fatal("invalid definition was saved")
	}
	for _, name := range []string{"../review", "/tmp/review", "a/b", ""} {
		if err := wt.SaveDefinition(name, editable, ""); err == nil {
			t.Errorf("unsafe name %q accepted", name)
		}
	}
}

func TestDefinitionEditPreservesSourceFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-filename.md")
	if err := os.WriteFile(path, []byte(editable), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewWorkflowTool()
	wt.SetSearchPaths([]string{dir})
	changed := editable + " carefully"
	if err := wt.SaveDefinition("review", changed, editable); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != changed {
		t.Fatal("original project definition was not updated")
	}
	if _, err := os.Stat(filepath.Join(dir, "review.md")); !os.IsNotExist(err) {
		t.Fatal("save created a duplicate definition")
	}
}

func TestDefinitionSaveMakesProjectCopyWithoutChangingUserSource(t *testing.T) {
	project, user := t.TempDir(), t.TempDir()
	path := filepath.Join(user, "review.md")
	if err := os.WriteFile(path, []byte(editable), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewWorkflowTool()
	wt.SetSearchPaths([]string{project, user})
	changed := editable + " carefully"
	if err := wt.SaveDefinition("review", changed, editable); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != editable {
		t.Fatal("user workflow source was modified")
	}
	if got, _ := wt.Definition("review"); got != changed {
		t.Fatal("project override was not selected")
	}
}

func TestDefinitionSaveDoesNotOverwriteDifferentNamedFile(t *testing.T) {
	dir := t.TempDir()
	other := strings.Replace(editable, "name: review", "name: another", 1)
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := NewWorkflowTool()
	wt.SetSearchPaths([]string{dir})
	if err := wt.SaveDefinition("review", editable, ""); err == nil {
		t.Fatal("save overwrote a different workflow sharing the destination filename")
	}
}
