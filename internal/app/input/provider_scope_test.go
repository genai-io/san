package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
)

func scopeSelector(store *llm.Store, provider string) ProviderSelector {
	s := NewProviderSelector()
	s.store, s.active, s.activeTab = store, true, providerTabModels
	s.allModels = []providerModelItem{
		{ID: "marked", ProviderName: provider, AuthMethod: llm.AuthAPIKey},
		{ID: "highlighted", ProviderName: provider, AuthMethod: llm.AuthSubscription},
	}
	s.visibleItems = []providerListItem{{Kind: providerItemModel, Model: &s.allModels[0]}, {Kind: providerItemModel, Model: &s.allModels[1]}}
	return s
}

func scopeKeySelection(t *testing.T, s *ProviderSelector, key tea.KeyPressMsg) providerModelSelectedMsg {
	t.Helper()
	cmd := s.HandleKeypress(key)
	if cmd == nil {
		t.Fatal("selection returned no command")
	}
	msg, ok := cmd().(providerModelSelectedMsg)
	if !ok {
		t.Fatal("selection returned unexpected message")
	}
	return msg
}

func TestProviderScopeKeys(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "Enter", true: "Global"}[global], func(t *testing.T) {
			s := scopeSelector(nil, "scope-keys")
			s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeySpace})
			s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
			key := tea.KeyPressMsg{Code: tea.KeyEnter}
			want := "marked"
			if global {
				key = tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
				want = "highlighted"
			}
			msg := scopeKeySelection(t, &s, key)
			if msg.ModelID != want || msg.UserDefault != global {
				t.Fatalf("selection = %+v", msg)
			}
			if global && msg.AuthMethod != llm.AuthSubscription {
				t.Fatal("lost highlighted auth method")
			}
		})
	}
	for _, mode := range []string{"providers", "custom", "ollama", "credentials", "remove", "search"} {
		t.Run(mode, func(t *testing.T) {
			s := scopeSelector(nil, "scope-keys")
			key := tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
			switch mode {
			case "providers":
				s.activeTab = providerTabProviders
			case "custom":
				s.customFormActive = true
			case "ollama":
				s.ollamaFormActive = true
			case "credentials":
				s.openCredentialForm(providerAuthMethodItem{EnvVars: []string{"SAN_SCOPE_TEST_KEY"}}, 0, 0)
			case "remove":
				s.confirmRemoveActive = true
			case "search":
				key = tea.KeyPressMsg{Code: 'g', Text: "g"}
			}
			if cmd := s.HandleKeypress(key); cmd != nil {
				if msg := cmd(); msg != nil {
					if _, ok := msg.(providerModelSelectedMsg); ok {
						t.Fatal("guard emitted model selection")
					}
				}
			}
			if mode == "search" && s.searchQuery != "g" {
				t.Fatalf("search = %q", s.searchQuery)
			}
		})
	}
}

func TestProviderGlobalScopeHandler(t *testing.T) {
	for _, mode := range []string{"project", "inherited", "failed-stop"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("SAN_TRUST_WORKSPACE", "1")
			store, err := llm.NewStoreForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetCurrentModel("old", llm.OpenAI, llm.AuthAPIKey); err != nil {
				t.Fatal(err)
			}
			if mode == "project" {
				if err := store.SetCurrentModel("project", llm.Moonshot, llm.AuthAPIKey); err != nil {
					t.Fatal(err)
				}
			}
			shared, err := llm.NewStoreForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			id := llm.ProviderID("scope-global-" + mode)
			llm.Register(llm.Meta{Provider: id, AuthMethod: llm.AuthSubscription}, func(context.Context) (llm.Provider, error) { return &connectFailProvider{}, nil })
			t.Cleanup(func() { llm.Unregister(id, llm.AuthSubscription) })
			state := ProviderState{Selector: scopeSelector(store, string(id))}
			state.Selector.selectedIdx = 1
			current := store.CurrentModel()
			switches, sets, reloads := 0, 0, 0
			conversation := conv.NewModel(80)
			deps := OverlayDeps{
				Conv:           &conversation.ConversationModel,
				CommitMessages: func() []tea.Cmd { return nil },
				ReloadModelStore: func() {
					reloads++
					if err := shared.Reload(); err != nil {
						t.Fatal(err)
					}
				},
				SetCurrentModel: func(info *llm.CurrentModelInfo) { sets++; current = info },
				SwitchProvider: func(_ llm.Provider, commit func() error) error {
					switches++
					if store.UserDefaultModel().ModelID != "old" || current.ModelID != "old" {
						t.Fatal("changed selection before stop")
					}
					if mode == "failed-stop" {
						return context.DeadlineExceeded
					}
					return commit()
				},
			}
			var before []byte
			projectPath := filepath.Join(cwd, ".san", "providers.json")
			if mode == "project" {
				before, err = os.ReadFile(projectPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			msg := scopeKeySelection(t, &state.Selector, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			if _, handled := UpdateProvider(deps, &state, msg); !handled {
				t.Fatal("selection unhandled")
			}
			persisted, err := llm.NewStore()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "failed-stop" {
				if switches != 1 || sets != 0 || current.ModelID != "old" || persisted.UserDefaultModel().ModelID != "old" {
					t.Fatal("failed stop changed model/default")
				}
				return
			}
			want := llm.CurrentModelInfo{ModelID: "highlighted", Provider: id, AuthMethod: llm.AuthSubscription}
			if *persisted.UserDefaultModel() != want {
				t.Fatalf("default = %+v", persisted.UserDefaultModel())
			}
			if mode == "project" {
				after, err := os.ReadFile(projectPath)
				if err != nil {
					t.Fatal(err)
				}
				if switches != 0 || sets != 0 || reloads != 1 || current.ModelID != "project" || shared.CurrentModel().ModelID != "project" || *shared.UserDefaultModel() != want || string(before) != string(after) {
					t.Fatal("global selection changed project or failed reload")
				}
				if len(conversation.Messages) != 1 || !strings.Contains(conversation.Messages[0].Content, "project unchanged") {
					t.Fatalf("missing persistent scope confirmation: %+v", conversation.Messages)
				}
			} else if switches != 1 || sets != 1 || *current != want {
				t.Fatal("global selection did not activate model")
			}
		})
	}
}

func TestProviderGlobalScopeProjectReadFailure(t *testing.T) {
	for _, hasOverride := range []bool{false, true} {
		t.Run(map[bool]string{false: "inherited", true: "project"}[hasOverride], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("SAN_TRUST_WORKSPACE", "1")
			cwd := t.TempDir()
			store, err := llm.NewStoreForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetCurrentModel("default", llm.OpenAI, llm.AuthAPIKey); err != nil {
				t.Fatal(err)
			}
			wantActive, wantDefault := "default", "default"
			if hasOverride {
				if err := store.SetCurrentModel("project", llm.OpenAI, llm.AuthAPIKey); err != nil {
					t.Fatal(err)
				}
				wantActive, wantDefault = "project", "highlighted"
			}
			path := filepath.Join(cwd, ".san", "providers.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			const malformed = `{"current":`
			if err := os.WriteFile(path, []byte(malformed), 0o644); err != nil {
				t.Fatal(err)
			}
			state := ProviderState{Selector: scopeSelector(store, "openai")}
			state.Selector.selectedIdx = 1
			conversation := conv.NewModel(80)
			deps := OverlayDeps{
				Conv:           &conversation.ConversationModel,
				CommitMessages: func() []tea.Cmd { return nil },
				SwitchProvider: func(llm.Provider, func() error) error {
					t.Fatal("unreadable project scope must not switch the active model")
					return nil
				},
				SetCurrentModel: func(*llm.CurrentModelInfo) { t.Fatal("active model changed") },
			}
			msg := scopeKeySelection(t, &state.Selector, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			UpdateProvider(deps, &state, msg)
			user, err := llm.NewStore()
			if err != nil {
				t.Fatal(err)
			}
			if got := user.UserDefaultModel(); got == nil || got.ModelID != wantDefault {
				t.Fatalf("user default = %+v, want %s; notices: %+v", got, wantDefault, conversation.Messages)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != malformed || store.CurrentModel().ModelID != wantActive {
				t.Fatal("global update changed project configuration or active model")
			}
			if len(conversation.Messages) != 1 || hasOverride != strings.Contains(conversation.Messages[0].Content, "project unchanged") {
				t.Fatalf("unexpected scope feedback: %+v", conversation.Messages)
			}
		})
	}
}

func TestProviderScopeFooter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	store, err := llm.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	s := scopeSelector(store, "scope-footer")
	if hints := s.renderHints(); !strings.Contains(hints, "Enter user default") || !strings.Contains(hints, "Ctrl+G user default") {
		t.Fatalf("empty default hints = %q", hints)
	}
	if err := store.SetUserDefaultModel("default", llm.OpenAI, llm.AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	if hints := s.renderHints(); !strings.Contains(hints, "Enter project") || !strings.Contains(hints, "Ctrl+G user default") {
		t.Fatalf("hints = %q", hints)
	}
	s.activeTab = providerTabProviders
	if strings.Contains(s.renderHints(), "Ctrl+G") {
		t.Fatal("Providers tab advertises global selection")
	}
}
