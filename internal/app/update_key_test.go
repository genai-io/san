package app

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

	cmd, handled := m.handleTextareaShortcut(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !handled {
		t.Fatal("Ctrl+T was not handled")
	}
	if cmd == nil {
		t.Fatal("Ctrl+T should return a status timer command")
	}
	if m.env.ThinkingEffort != "low" {
		t.Fatalf("ThinkingEffort = %q, want low", m.env.ThinkingEffort)
	}
	if m.userInput.Provider.StatusMessage != "thinking: low" {
		t.Fatalf("StatusMessage = %q, want thinking: low", m.userInput.Provider.StatusMessage)
	}
	if m.conv.ShowTasks {
		t.Fatal("Ctrl+T should not toggle the task panel")
	}
}

func TestAltTTogglesTaskPanel(t *testing.T) {
	m := &model{}

	_, handled := m.handleTextareaShortcut(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'t'},
		Alt:   true,
	})
	if !handled {
		t.Fatal("Alt+T was not handled")
	}
	if !m.conv.ShowTasks {
		t.Fatal("Alt+T should toggle the task panel")
	}
}
