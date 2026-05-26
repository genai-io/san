package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/core/system"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/tool"
)

// BuildParams contains all values needed to construct a core.Agent.
// The app layer assembles this from env, services, and workspace state.
type BuildParams struct {
	Provider       llm.Provider
	ModelID        string
	MaxTokens      int
	ThinkingEffort string

	CWD     string
	CWDFunc func() string // dynamic CWD for tool execution; falls back to CWD if nil
	IsGit   bool

	// AgentDirectory, when non-nil, supplies the available-agents listing
	// embedded into the Agent tool's description. Returning an empty string
	// hides the listing entirely (used by subagent contexts to discourage
	// recursive spawning).
	AgentDirectory func() string

	// IdentityText, when non-empty, replaces the default identity slot with
	// a user-defined persona. Sourced from ~/.gen/identities/<name>.md.
	IdentityText string

	DisabledTools map[string]bool
	MCPTools      []core.Tool

	PermissionDecider PermDecisionFunc
	InteractionFunc   tool.InteractionFunc
	ToolProgress      func(toolCallID string, msg string)

	// OnEvent observes every agent lifecycle event synchronously, alongside
	// outbox delivery. Used by the trace recorder; nil leaves recording off.
	OnEvent func(core.Event)
}

func buildAgent(p BuildParams) (core.Agent, *PermissionBridge, error) {
	if p.Provider == nil {
		return nil, nil, fmt.Errorf("no LLM provider configured")
	}

	client := llm.NewClient(p.Provider, p.ModelID, p.MaxTokens)
	client.SetThinkingEffort(p.ThinkingEffort)

	sys := system.Build(core.ScopeMain,
		system.WithProvider(client.Name()),
		system.WithIdentity(p.IdentityText),
		system.WithGitGuidelines(p.IsGit),
		system.WithEnvironment(system.Environment{
			Cwd:     p.CWD,
			IsGit:   p.IsGit,
			ModelID: client.ModelID(),
		}),
	)

	cwdFunc := p.CWDFunc
	if cwdFunc == nil {
		cwd := p.CWD
		cwdFunc = func() string { return cwd }
	}

	schemas := (&tool.Set{
		Disabled:       p.DisabledTools,
		AgentDirectory: p.AgentDirectory,
	}).Tools()
	var adaptOpts []tool.AdaptOption
	if p.InteractionFunc != nil {
		adaptOpts = append(adaptOpts, tool.WithInteraction(p.InteractionFunc))
	}
	if p.ToolProgress != nil {
		adaptOpts = append(adaptOpts, tool.WithToolProgress(p.ToolProgress))
	}
	pb := NewPermissionBridge(p.PermissionDecider)
	var ag core.Agent
	adaptOpts = append(adaptOpts, tool.WithMessagesGetterProvider(func() []core.Message {
		if ag == nil {
			return nil
		}
		return ag.Messages()
	}))
	tools := tool.AdaptToolRegistry(schemas, cwdFunc, adaptOpts...)
	for _, t := range p.MCPTools {
		tools.Add(t, "mcp:"+t.Name())
	}

	compactClient := client
	compactFunc := func(ctx context.Context, msgs []core.Message) (string, error) {
		text := core.BuildCompactionText(msgs)
		resp, err := compactClient.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(text, nil)}, core.CompactMaxTokens)
		if err != nil {
			return "", err
		}
		summary := strings.TrimSpace(resp.Content)
		if summary == "" {
			return "", fmt.Errorf("compaction produced empty summary")
		}
		return summary, nil
	}

	ag = core.NewAgent(core.Config{
		ID:          "main",
		LLM:         client,
		System:      sys,
		Tools:       tool.WithPermission(tools, pb.PermissionFunc()),
		CompactFunc: compactFunc,
		CWD:         p.CWD,
		OnEvent:     p.OnEvent,
	})

	return ag, pb, nil
}
