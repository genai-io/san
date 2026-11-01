package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/tool"
)

func TestProviderSelectionStopTimeoutPreservesSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	old := &llm.CurrentModelInfo{ModelID: "old", Provider: llm.OpenAI, AuthMethod: llm.AuthAPIKey}
	next := &llm.CurrentModelInfo{ModelID: "next", Provider: llm.Moonshot, AuthMethod: llm.AuthAPIKey}
	previous := &switchProviderStub{name: "openai"}
	selected := &switchProviderStub{name: "moonshot"}
	if err := store.SetThinkingEffort("next", "high"); err != nil {
		t.Fatal(err)
	}
	selector := input.NewProviderSelector()
	if _, err := selector.SetModel(old.ModelID, string(old.Provider), old.AuthMethod); err != nil {
		t.Fatal(err)
	}
	// The store was opened before the selector persisted its selection.
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	m := fixedComposerModel(80, 24)
	m.services.Skill = skill.NewRegistry()
	m.services.Tool = tool.NewRegistry()
	m.env.CachedUserInstructions = "test instructions"
	m.services.LLM = &llm.Conn{}
	m.services.LLM.SetCurrentModel(old)
	m.services.LLM.SetProvider(previous)
	m.env.CurrentModel, m.env.LLMProvider = old, previous
	m.env.store, m.env.ThinkingEffort = store, "low"
	sess := &agent.Runtime{}
	m.services.Agent = sess
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer stopRuntime(t, sess)
	defer unblock()
	if err := sess.Start(agent.BuildParams{Provider: previous, ModelID: "old", OnEvent: func(ev core.Event) {
		if _, ok := ev.(core.AgentStarted); ok {
			close(started)
			<-release
		}
	}}, nil); err != nil {
		t.Fatal(err)
	}
	<-started
	deps := m.overlayDeps()
	commits := 0
	commit := func() error {
		commits++
		if _, err := selector.SetModel(next.ModelID, string(next.Provider), next.AuthMethod); err != nil {
			return err
		}
		deps.SetCurrentModel(next)
		return nil
	}
	if err := deps.SwitchProvider(selected, commit); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("switch = %v", err)
	}
	if commits != 0 || m.env.CurrentModel != old || m.env.LLMProvider != previous || m.services.LLM.CurrentModel() != old || m.services.LLM.Provider() != previous || m.env.ThinkingEffort != "low" {
		t.Fatal("failed stop changed selection")
	}
	persisted, err := llm.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.CurrentModel(); got == nil || *got != *old {
		t.Fatalf("persisted selection = %+v", got)
	}
	unblock()
	if err := deps.SwitchProvider(selected, commit); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || m.env.CurrentModel != next || m.env.LLMProvider != selected || m.services.LLM.CurrentModel() != next || m.services.LLM.Provider() != selected || m.env.ThinkingEffort != "high" {
		t.Fatal("successful selection did not commit once")
	}
	if err := persisted.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := persisted.CurrentModel(); got == nil || *got != *next {
		t.Fatalf("persisted selection = %+v", got)
	}
}
