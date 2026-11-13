package subagent

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type namedHookProvider struct {
	continuationProvider
	name string
}

func (p namedHookProvider) Name() string { return p.name }

func TestChildHooksFollowSelectedProviderAndFallbackWithoutRecreatingHandler(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected", true: "fallback"}[fallback], func(t *testing.T) {
			store, parent := continuationSessions(t, "owner")
			factoryCalls, rebindCalls, cleanups, starts, stops := 0, 0, 0, 0, 0
			var boundProvider llm.Provider
			boundModel := ""
			childResponse := aitest.Says("child answer")
			if fallback {
				childResponse = aitest.Fails(errors.New("model_not_found"))
			}
			child := namedHookProvider{continuationProvider{aitest.New(childResponse)}, "child-provider"}
			parentDriver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				if boundProvider == nil || boundProvider.Name() != "parent-provider" || boundModel != "parent-model" || rebindCalls != 1 || factoryCalls != 1 || cleanups != 0 {
					t.Errorf("fallback dispatched before same-handler model rebind: provider=%v model=%s factory=%d rebind=%d cleanup=%d", boundProvider, boundModel, factoryCalls, rebindCalls, cleanups)
				}
				return aitest.Says("fallback answer")(ctx, req)
			})
			parentProvider := namedHookProvider{continuationProvider{parentDriver}, "parent-provider"}
			e := NewExecutor(parentProvider, t.TempDir(), "parent-model", nil)
			e.SetResolver(&stubResolver{provider: child})
			if err := e.SetSessions(store, parent); err != nil {
				t.Fatal(err)
			}
			e.SetChildHooks(func(owner *session.Session, provider llm.Provider, model string) (hook.Handler, func(llm.Provider, string), func()) {
				factoryCalls++
				if owner == parent || provider.Name() != "child-provider" || model != "selected-model" {
					t.Errorf("factory received parent instead of selected child: owner=%s provider=%s model=%s", owner.ID(), provider.Name(), model)
				}
				boundProvider, boundModel = provider, model
				handler := childStartHooks{dispatch: func(event hook.EventType) {
					switch event {
					case hook.SubagentStart:
						starts++
						if boundProvider.Name() != "child-provider" || boundModel != "selected-model" {
							t.Error("start hook lost selected model")
						}
					case hook.SubagentStop:
						stops++
						wantProvider, wantModel := "child-provider", "selected-model"
						if fallback {
							wantProvider, wantModel = "parent-provider", "parent-model"
						}
						if boundProvider.Name() != wantProvider || boundModel != wantModel {
							t.Errorf("stop hook model=%s/%s", boundProvider.Name(), boundModel)
						}
					}
				}}
				return handler, func(provider llm.Provider, model string) {
						rebindCalls++
						snapshot := owner.Snapshot()
						config := snapshot.Config()
						if config.Provider == nil || *config.Provider != provider.Name() || config.Model == nil || *config.Model != model {
							t.Error("hook rebind preceded committed fallback configuration")
						}
						boundProvider, boundModel = provider, model
					}, func() {
						cleanups++
						if stops != 1 {
							t.Error("cleanup preceded child stop")
						}
					}
			})
			result, err := e.Run(context.Background(), tool.AgentExecRequest{Prompt: "work", Model: "openai/selected-model", ParentToolCallID: "launch"})
			if err != nil {
				t.Fatal(err)
			}
			want := "child answer"
			wantRebind := 0
			if fallback {
				want = "fallback answer"
				wantRebind = 1
			}
			if result.Content != want || factoryCalls != 1 || rebindCalls != wantRebind || cleanups != 1 || starts != 1 || stops != 1 {
				t.Fatalf("handler lifetime changed across fallback: result=%+v factory=%d rebind=%d cleanup=%d starts=%d stops=%d", result, factoryCalls, rebindCalls, cleanups, starts, stops)
			}
		})
	}
}
