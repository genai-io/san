package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/tool"
	toolworkflow "github.com/genai-io/san/internal/tool/workflow"
)

func TestWorkflowSuggestionsWalkCommandsThenSavedNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("---\nname: demo\ndescription: Example workflow\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nUse {{input.topic}} and {{input.style}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := toolworkflow.NewWorkflowTool()
	wt.SetSearchPaths([]string{dir})
	tools := tool.NewRegistry()
	tools.Register(wt)
	match := commandSuggestionMatcher(&command.Registry{}, tools)
	names := func(query string) []string {
		var out []string
		for _, item := range match(query) {
			out = append(out, item.Name)
		}
		return out
	}
	if got := names("/workflow "); !slices.Equal(got, []string{"workflow list", "workflow show", "workflow run", "workflow stop"}) {
		t.Fatalf("workflow subcommands = %v", got)
	}
	for _, query := range []string{"/workflow show ", "/workflow run de"} {
		verb := "show"
		if query == "/workflow run de" {
			verb = "run"
		}
		if got := names(query); !slices.Equal(got, []string{"workflow " + verb + " demo"}) {
			t.Errorf("%s -> %v", query, got)
		}
	}
	if got := names("/workflow run demo "); !slices.Equal(got, []string{"workflow run demo style=", "workflow run demo topic="}) {
		t.Errorf("input keys = %v", got)
	}
	if got := names("/workflow run demo topic="); got != nil {
		t.Errorf("inputs should stay free-form, got %v", got)
	}
	if got := names("/workflow run demo topic=cache "); !slices.Equal(got, []string{"workflow run demo topic=cache style="}) {
		t.Errorf("remaining input keys = %v", got)
	}

	state := suggest.NewState(match)
	state.UpdateSuggestions("/workflow ")
	if selected := state.Selected(); selected != "/workflow list" {
		t.Fatalf("selected = %q", selected)
	}
	state.MoveDown()
	state.UpdateSuggestions("/workflow show ")
	if selected := state.Selected(); selected != "/workflow show demo" {
		t.Fatalf("saved name selected = %q", selected)
	}
}
