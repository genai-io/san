package input

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
)

func TestProviderSelectionCommitsOnlyInsideSwitch(t *testing.T) {
	store := newProviderTestStore(t)
	id := llm.ProviderID("selection-gate-test")
	llm.Register(llm.Meta{Provider: id, AuthMethod: llm.AuthAPIKey}, func(context.Context) (llm.Provider, error) { return &connectFailProvider{}, nil })
	t.Cleanup(func() { llm.Unregister(id, llm.AuthAPIKey) })
	state := ProviderState{Selector: NewProviderSelector()}
	state.Selector.store = store
	if _, err := state.Selector.SetModel("old", "openai", llm.AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	current := store.CurrentModel()
	conversation := conv.NewModel(80)
	calls, commits, projections := 0, 0, 0
	deps := OverlayDeps{
		Conv:            &conversation.ConversationModel,
		CommitMessages:  func() []tea.Cmd { projections++; return nil },
		SetCurrentModel: func(info *llm.CurrentModelInfo) { commits++; current = info },
		SwitchProvider: func(_ llm.Provider, commit func() error) error {
			calls++
			if current.ModelID != "old" || store.CurrentModel().ModelID != "old" {
				t.Fatal("selection changed before stop")
			}
			if calls == 1 {
				return context.DeadlineExceeded
			}
			return commit()
		},
	}
	msg := providerModelSelectedMsg{ModelID: "next", ProviderName: string(id), AuthMethod: llm.AuthAPIKey}
	handleProviderModelSelected(deps, &state, msg)
	if commits != 0 || current.ModelID != "old" || store.CurrentModel().ModelID != "old" {
		t.Fatal("failed stop changed selection")
	}
	handleProviderModelSelected(deps, &state, msg)
	if calls != 2 || commits != 1 || projections != 2 || current.ModelID != "next" || store.CurrentModel().ModelID != "next" {
		t.Fatal("selection not committed exactly once")
	}
}

func TestModelCommandScopesSelectionToCurrentProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	t.Chdir(t.TempDir()) // The TUI's cwd can differ from the process cwd.
	user, err := llm.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := user.SetCurrentModel("default", llm.OpenAI, llm.AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	if err := user.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{
		{ID: "default"}, {ID: "project-a"}, {ID: "project-b"},
	}); err != nil {
		t.Fatal(err)
	}
	state := &Model{Provider: ProviderState{Selector: NewProviderSelector()}}
	projectA, projectB := t.TempDir(), t.TempDir()
	for _, step := range []struct {
		cwd, want, selectModel string
	}{
		{projectA, "default", "project-a"},
		{projectB, "default", "project-b"},
		{projectA, "project-a", ""},
		{projectB, "project-b", ""},
	} {
		c := NewSlashCommandController(SlashCommandEnv{Input: state, Cwd: step.cwd, Width: 80, Height: 24})
		if _, _, err := c.handleModelCommand(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
		selector := &state.Provider.Selector
		if got := selector.store.CurrentModel(); got == nil || got.ModelID != step.want {
			t.Fatalf("project %s: current = %+v, want %s", step.cwd, got, step.want)
		}
		marked := ""
		for _, model := range selector.allModels {
			if model.IsCurrent {
				marked = model.ID
			}
		}
		if marked != step.want {
			t.Fatalf("picker marks %q, want %q", marked, step.want)
		}
		if step.selectModel != "" {
			if _, err := selector.SetModel(step.selectModel, "openai", llm.AuthAPIKey); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := user.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := user.CurrentModel(); got == nil || got.ModelID != "default" {
		t.Fatalf("project selections changed user default: %+v", got)
	}
}
