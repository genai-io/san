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
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`/workflow run demo topic="two `, nil},
		{`/workflow run demo topic='two `, nil},
		{`/workflow run demo topic="see style=brief" `, []string{`workflow run demo topic="see style=brief" style=`}},
		{`/workflow run demo topic='see style=brief' st`, []string{`workflow run demo topic='see style=brief' style=`}},
		{"/workflow run demo topic=cache\tst", []string{"workflow run demo topic=cache\tstyle="}},
	} {
		if got := names(tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("%q -> %q, want %q", tc.query, got, tc.want)
		}
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

func TestWorkflowSuggestionsQuoteSavedNames(t *testing.T) {
	dir := t.TempDir()
	body := "---\nname: release review\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nUse {{input.topic}} and {{input.style}}\n"
	if err := os.WriteFile(filepath.Join(dir, "release.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := toolworkflow.NewWorkflowTool()
	wt.SetSearchPaths([]string{dir})
	tools := tool.NewRegistry()
	tools.Register(wt)
	match := commandSuggestionMatcher(&command.Registry{}, tools)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`/workflow show rel`, []string{`workflow show "release review"`}},
		{`/workflow run rel`, []string{`workflow run "release review"`}},
		{`/workflow run "release `, []string{`workflow run "release review"`}},
		{`/workflow run 'release `, []string{`workflow run "release review"`}},
		{`/workflow show "release review" `, nil},
		{`/workflow run "release review" `, []string{`workflow run "release review" style=`, `workflow run "release review" topic=`}},
		{`/workflow run 'release review' topic="see style=brief" st`, []string{`workflow run "release review" topic="see style=brief" style=`}},
	} {
		var got []string
		for _, suggestion := range match(tc.query) {
			got = append(got, suggestion.Name)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q -> %q, want %q", tc.query, got, tc.want)
		}
	}
}
