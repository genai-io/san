package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
)

type testThinkingProvider struct {
	efforts []string
	def     string
}

func (p *testThinkingProvider) Stream(context.Context, llm.CompletionOptions) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch
}

func (p *testThinkingProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (p *testThinkingProvider) Name() string { return "test" }

func (p *testThinkingProvider) ThinkingEfforts(string) []string {
	out := make([]string, len(p.efforts))
	copy(out, p.efforts)
	return out
}

func (p *testThinkingProvider) DefaultThinkingEffort(string) string { return p.def }

func TestCtrlTCyclesThinkingEffort(t *testing.T) {
	m := &model{}
	m.env.LLMProvider = &testThinkingProvider{
		efforts: []string{"none", "low", "medium", "high"},
		def:     "none",
	}
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "test-model", Provider: llm.OpenAI}

	cmd, handled := m.handleTextareaShortcut(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl+T was not handled")
	}
	if cmd != nil {
		t.Fatal("Ctrl+T should update the model label without a status message")
	}
	if m.env.ThinkingEffort != "low" {
		t.Fatalf("ThinkingEffort = %q, want low", m.env.ThinkingEffort)
	}
	if m.userInput.Provider.StatusMessage != "" {
		t.Fatalf("StatusMessage = %q, want empty", m.userInput.Provider.StatusMessage)
	}
	if m.conv.ShowTasks {
		t.Fatal("Ctrl+T should not toggle the task panel")
	}
}

func TestCtrlTUsesCachedModelReasoningMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthSubscription, []llm.ModelInfo{{
		ID:        "dynamic-model",
		Reasoning: llm.NewReasoningCapability([]string{"low", "ultra"}, "low"),
	}}); err != nil {
		t.Fatalf("CacheModels() error = %v", err)
	}

	m := &model{}
	m.env.store = store
	m.env.LLMProvider = &testThinkingProvider{
		efforts: []string{"high"},
		def:     "high",
	}
	m.env.CurrentModel = &llm.CurrentModelInfo{
		ModelID:    "dynamic-model",
		Provider:   llm.OpenAI,
		AuthMethod: llm.AuthSubscription,
	}

	cmd, handled := m.handleTextareaShortcut(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if !handled || cmd != nil {
		t.Fatal("Ctrl+T should be handled without a status timer")
	}
	if m.env.ThinkingEffort != "ultra" {
		t.Fatalf("ThinkingEffort = %q, want dynamic next effort ultra", m.env.ThinkingEffort)
	}
}

func TestAltTTogglesTaskPanel(t *testing.T) {
	m := &model{}

	_, handled := m.handleTextareaShortcut(tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt})
	if !handled {
		t.Fatal("Alt+T was not handled")
	}
	if !m.conv.ShowTasks {
		t.Fatal("Alt+T should toggle the task panel")
	}
}
