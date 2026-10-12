package subagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/app"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type headlessModelProvider struct {
	*testutil.FakeProvider
	mu    sync.Mutex
	model string
}

func (p *headlessModelProvider) Client(model string, headers map[string]string) (*ai.Client, error) {
	p.mu.Lock()
	p.model = model
	p.mu.Unlock()
	return p.FakeProvider.Client(model, headers)
}

func TestHeadlessAgentLoadsConfiguredWorkerModel(t *testing.T) {
	originalRegistry := subagent.Default()
	t.Cleanup(func() { subagent.SetDefaultRegistry(originalRegistry) })
	for _, tc := range []struct{ name, userModel, projectModel, want string }{
		{"user configuration", "user-worker", "", "user-worker"},
		{"project override", "user-worker", "project-worker", "project-worker"},
		{"explicit inheritance", "user-worker", "inherit", "parent-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Chdir(cwd)
			for dir, model := range map[string]string{home: tc.userModel, cwd: tc.projectModel} {
				if model == "" {
					continue
				}
				configDir := filepath.Join(dir, ".san")
				if err := os.MkdirAll(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(map[string]string{"subagentModel": model})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(configDir, "settings.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			providerID := llm.ProviderID("headless-worker-model-test")
			provider := &headlessModelProvider{FakeProvider: &testutil.FakeProvider{Responses: []llm.CompletionResponse{testutil.EndTurnResponse("done")}}}
			llm.Register(llm.Meta{Provider: providerID, AuthMethod: llm.AuthAPIKey}, func(context.Context) (llm.Provider, error) { return provider, nil })
			t.Cleanup(func() { llm.Unregister(providerID, llm.AuthAPIKey) })
			store, err := llm.NewStore()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetCurrentModel("parent-model", providerID, llm.AuthAPIKey); err != nil {
				t.Fatal(err)
			}
			if err := app.RunAgent(app.AgentRunOptions{Prompt: "Inspect configuration"}); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			got := provider.model
			provider.mu.Unlock()
			if got != tc.want {
				t.Fatalf("headless worker model = %q, want %q", got, tc.want)
			}
		})
	}
}
