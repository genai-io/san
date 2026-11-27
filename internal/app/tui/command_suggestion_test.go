package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/genai-io/san/internal/app/tui/widgets/suggest"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

func TestFlowSchedulerIsWiredDirectlyToSlashCommands(t *testing.T) {
	manager := task.NewRunSet()
	svc := services{Task: manager, Flow: flow.NewScheduler(manager)}
	if svc.Flow == nil {
		t.Fatal("app services must construct a direct Flow service")
	}
	m := &model{services: svc}
	if got := m.slashCommandEnv().Flow; got != svc.Flow {
		t.Fatal("slash commands did not receive the app-owned Flow service")
	}
}

func TestFlowStorageDoesNotRequireAProviderOrToolRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manager := task.NewRunSet()
	m := &model{services: services{Task: manager, Flow: flow.NewScheduler(manager)}}
	m.env.CWD = t.TempDir()
	m.ReconfigureAgentTool()
	source := "---\nname: offline\n---\n```mermaid\nflowchart LR\n work\n```\n## work\nInspect {{input}}\n"
	if err := m.services.Flow.SaveDefinition("offline", source, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := m.services.Flow.Definition("offline"); err != nil || got != source {
		t.Fatalf("offline definition=%q error=%v", got, err)
	}
	match := commandSuggestionMatcher(&command.Registry{}, m.services.Flow)
	if got := match("/flow run offline "); len(got) != 1 || got[0].Name != "flow run offline input=" || got[0].Description != "Value for run input: input" {
		t.Fatalf("bare run input suggestion: %+v", got)
	}
	if got := (&model{}).slashCommandEnv().Flow; got != nil {
		t.Fatal("missing service should not become a typed nil interface")
	}
}

func TestFlowSuggestionsWalkCommandsThenSavedNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("---\nname: demo\ndescription: Example flow\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nUse {{input.topic}} and {{input.style}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flows := flow.NewScheduler(task.NewRunSet())
	flows.SetSearchPaths([]string{dir})
	match := commandSuggestionMatcher(&command.Registry{}, flows)
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
	flows := flow.NewScheduler(task.NewRunSet())
	flows.SetSearchPaths([]string{dir})
	match := commandSuggestionMatcher(&command.Registry{}, flows)
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
