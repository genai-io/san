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
