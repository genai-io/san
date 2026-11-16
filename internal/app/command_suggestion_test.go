package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/tool"
	toolflow "github.com/genai-io/san/internal/tool/flow"
)

func TestFlowSuggestionsWalkCommandsThenSavedNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("---\nname: demo\ndescription: Example flow\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nUse {{input.topic}} and {{input.style}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := toolflow.NewFlowTool()
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
	if got := names("/flow "); !slices.Equal(got, []string{"flow list", "flow show", "flow run", "flow stop", "flow new", "flow from-skill", "flow edit", "flow inspect"}) {
		t.Fatalf("flow subcommands = %v", got)
	}
	for _, query := range []string{"/flow show ", "/flow run de"} {
		verb := "show"
		if query == "/flow run de" {
			verb = "run"
		}
		if got := names(query); !slices.Equal(got, []string{"flow " + verb + " demo"}) {
			t.Errorf("%s -> %v", query, got)
		}
	}
	if got := names("/flow edit de"); !slices.Equal(got, []string{"flow edit demo"}) {
		t.Fatalf("edit names = %v", got)
	}
	if got := names("/flow edit demo "); got != nil {
		t.Fatalf("edit must not suggest run inputs: %v", got)
	}
	if got := names("/flow run demo "); !slices.Equal(got, []string{"flow run demo style=", "flow run demo topic="}) {
		t.Errorf("input keys = %v", got)
	}
	if got := names("/flow run demo topic="); got != nil {
		t.Errorf("inputs should stay free-form, got %v", got)
	}
	if got := names("/flow run demo topic=cache "); !slices.Equal(got, []string{"flow run demo topic=cache style="}) {
		t.Errorf("remaining input keys = %v", got)
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`/flow run demo topic="two `, nil},
		{`/flow run demo topic='two `, nil},
		{`/flow run demo topic="see style=brief" `, []string{`flow run demo topic="see style=brief" style=`}},
		{`/flow run demo topic='see style=brief' st`, []string{`flow run demo topic='see style=brief' style=`}},
		{"/flow run demo topic=cache\tst", []string{"flow run demo topic=cache\tstyle="}},
	} {
		if got := names(tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("%q -> %q, want %q", tc.query, got, tc.want)
		}
	}

	state := suggest.NewState(match)
	state.UpdateSuggestions("/flow ")
	if selected := state.Selected(); selected != "/flow list" {
		t.Fatalf("selected = %q", selected)
	}
	state.MoveDown()
	state.UpdateSuggestions("/flow show ")
	if selected := state.Selected(); selected != "/flow show demo" {
		t.Fatalf("saved name selected = %q", selected)
	}
}

func TestFlowSuggestionsQuoteSavedNames(t *testing.T) {
	dir := t.TempDir()
	body := "---\nname: release review\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nUse {{input.topic}} and {{input.style}}\n"
	if err := os.WriteFile(filepath.Join(dir, "release.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := toolflow.NewFlowTool()
	wt.SetSearchPaths([]string{dir})
	tools := tool.NewRegistry()
	tools.Register(wt)
	match := commandSuggestionMatcher(&command.Registry{}, tools)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`/flow show rel`, []string{`flow show "release review"`}},
		{`/flow run rel`, []string{`flow run "release review"`}},
		{`/flow run "release `, []string{`flow run "release review"`}},
		{`/flow run 'release `, []string{`flow run "release review"`}},
		{`/flow show "release review" `, nil},
		{`/flow run "release review" `, []string{`flow run "release review" style=`, `flow run "release review" topic=`}},
		{`/flow run 'release review' topic="see style=brief" st`, []string{`flow run "release review" topic="see style=brief" style=`}},
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
