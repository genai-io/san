package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestDefaultAgentModelComesFromConfiguration(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"", "parent-model"}, {"inherit", "parent-model"}, {"worker-model", "worker-model"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			executor := &Executor{parentModelID: "parent-model"}
			executor.SetDefaultModel(tc.model)
			for _, name := range []string{"", "task-label"} {
				req := tool.AgentExecRequest{Agent: name, Prompt: "Inspect"}
				run, err := executor.prepareRun(context.Background(), req)
				if err != nil || run.cfg.modelID != tc.want {
					t.Fatalf("configured model ignored: %+v, %v", run, err)
				}
				info, _, ok := NewExecutorAdapter(executor).ResolveAgentSelection(name)
				if !ok || info.Model != tc.model {
					t.Fatalf("approval model differs from configured model: %+v", info)
				}
			}
		})
	}
}

func TestNamedConfigurationStillOwnsItsModel(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}
	executor.SetDefaultModel("default-worker")
	run, err := executor.prepareRun(context.Background(), tool.AgentExecRequest{
		Agent: "reviewer", Prompt: "Inspect", ResolvedAgentConfig: &AgentConfig{Name: "reviewer", Model: "named-worker"},
	})
	if err != nil || run.cfg.modelID != "named-worker" {
		t.Fatalf("default model overrode named configuration: %+v, %v", run, err)
	}
}
