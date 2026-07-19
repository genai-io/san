package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
)

type switchProviderStub struct{ name string }

func (p *switchProviderStub) Stream(context.Context, llm.CompletionOptions) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch
}

func (p *switchProviderStub) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (p *switchProviderStub) Name() string { return p.name }

func TestSwitchProviderUpdatesConnectionUsedBySlashCommands(t *testing.T) {
	previous := &switchProviderStub{name: "openai:subscription"}
	selected := &switchProviderStub{name: "moonshot"}
	conn := &llm.Conn{}
	conn.SetProvider(previous)

	m := &model{
		env: env{
			LLMProvider: previous,
			CurrentModel: &llm.CurrentModelInfo{
				ModelID:  "kimi-k3",
				Provider: llm.Moonshot,
			},
		},
		services: services{
			LLM:  conn,
			Hook: hook.NewEngine(nil, "", "", ""),
		},
	}

	m.switchProvider(selected)

	if got := m.env.LLMProvider; got != selected {
		t.Fatalf("env provider = %v, want selected provider", got)
	}
	if got := m.slashCommandEnv().LLM.Provider(); got != selected {
		t.Fatalf("slash command provider = %v, want selected provider", got)
	}
}
