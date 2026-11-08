package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type mainSessionProvider struct {
	restartStubProvider
	client *ai.Client
}

func (p *mainSessionProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}

// Exercise the actual TUI composition, not a second params builder in a fixture.
func TestBuildAgentParamsNativeAuditsAndPaging(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(map[bool]string{true: "allow", false: "deny"}[allow], func(t *testing.T) {
			m, store := resumedModel(t, nil)
			const name = "mcp__fixture__output"
			data := setting.NewData()
			if allow {
				data.Permissions.Allow = []string{name}
			} else {
				data.Permissions.Deny = []string{name}
			}
			m.services.Setting = setting.New(data)
			m.services.MCP = mcp.NewManagerForTest(nil)
			m.services.Hook = hook.NewEngine(data, "wrong-owner", m.env.CWD, "")
			m.services.Hook.AddSessionFunctionHook(hook.PreToolUse, name, hook.FunctionHook{
				Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
					return hook.HookOutput{}, nil
				},
			})
			states, err := skill.NewStore(filepath.Join(t.TempDir(), "skills.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.services.Skill = skill.NewRegistryForTest(map[string]*skill.Skill{"fixture": {Name: "fixture", State: skill.StateEnable}}, states, states)
			m.env.LLMProvider = &mainSessionProvider{client: aitest.New(
				aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "native-call", Name: name, Input: `{}`})}, StopReason: ai.StopToolUse}),
				aitest.Says("finished"),
			).Client()}
			params, err := m.buildAgentParams()
			if err != nil {
				t.Fatal(err)
			}
			if params.SessionID != m.services.Session.ID() || params.HookEngine != m.services.Hook {
				t.Fatal("composition lost native owner or hook")
			}
			if err := m.services.Skill.SetState("fixture", skill.StateActive, setting.ScopeUser); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			full := strings.Repeat("界", 40000)
			params.MCPTools = []core.Tool{sdkagent.ToolFunc(name, "output", func(context.Context, struct{}) (sdkagent.Result, error) {
				calls.Add(1)
				return sdkagent.TextResult(full), nil
			})}
			if err := m.services.Agent.Start(params, nil); err != nil {
				t.Fatal(err)
			}
			if err := m.services.Agent.Send(core.UserMessage("run", nil)); err != nil {
				t.Fatal(err)
			}
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case ev := <-m.services.Agent.Outbox():
					if _, ok := ev.(core.TurnEnded); !ok {
						continue
					}
					rec := m.services.Session
					if err := rec.Err(); err != nil {
						t.Fatal(err)
					}
					bytes, err := os.ReadFile(rec.TranscriptPath())
					if err != nil {
						t.Fatal(err)
					}
					for _, marker := range []string{"permission.decided", "hook.fired", "skill.state.changed"} {
						if !strings.Contains(string(bytes), marker) {
							t.Fatalf("missing %s audit", marker)
						}
					}
					history, err := store.Read(rec.ID(), session.ReadOptions{})
					if err != nil || len(history.Messages) < 4 {
						t.Fatalf("native history: %v", err)
					}
					if allow {
						if calls.Load() != 1 || !strings.Contains(string(bytes), "Full output persisted") {
							t.Fatal("allowed output was not executed and paged")
						}
						blob, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(rec.TranscriptPath())), "blobs", "tool-result", rec.ID(), "native-call"))
						if err != nil || string(blob) != full {
							t.Fatalf("full native output lost: %v", err)
						}
					} else if calls.Load() != 0 {
						t.Fatal("denied tool executed")
					}
					return
				case <-deadline.C:
					t.Fatal("runtime did not finish")
				}
			}
		})
	}
}

func TestBuildAgentParamsRequiresPermissionOwner(t *testing.T) {
	for _, m := range []*model{{}, {services: services{Setting: setting.New(setting.NewData())}}} {
		if _, err := m.buildAgentParams(); err == nil {
			t.Fatal("accepted unavailable policy")
		}
	}
}

// Data-owner validation cannot rebind extensions: they are not its inputs.
// Runtime construction must likewise preserve the hook audit chosen by the host.
func TestRuntimeConstructionPreservesHostHookAudit(t *testing.T) {
	m, store := resumedModel(t, nil)
	owner := agent.MainSession{Session: m.services.Session, Store: store}
	var audits atomic.Int32
	hooks := hook.NewEngine(nil, owner.Session.ID(), m.env.CWD, owner.Session.TranscriptPath())
	hooks.SetAuditCallback(func(hook.HookFiredAudit) { audits.Add(1) })
	hooks.AddSessionFunctionHook(hook.PreToolUse, "mcp__fixture__output", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) { return hook.HookOutput{}, nil }})
	if err := owner.Validate(); err != nil {
		t.Fatal(err)
	}
	provider := &mainSessionProvider{client: aitest.New(aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "audit-call", Name: "mcp__fixture__output", Input: `{}`})}, StopReason: ai.StopToolUse}), aitest.Says("done")).Client()}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "fake", PermissionRules: (agent.PermissionPolicy{}).Decide, HookEngine: hooks, MCPTools: []core.Tool{sdkagent.ToolFunc("mcp__fixture__output", "output", func(context.Context, struct{}) (sdkagent.Result, error) { return sdkagent.TextResult("unused"), nil })}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.services.Agent.Send(core.UserMessage("run", nil)); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-m.services.Agent.Outbox():
			if _, ok := ev.(core.TurnEnded); ok {
				if audits.Load() != 1 {
					t.Fatalf("host audit count = %d", audits.Load())
				}
				return
			}
		case <-deadline.C:
			t.Fatal("runtime did not finish")
		}
	}
}
