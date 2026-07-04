package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/tool"
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

	// Persona overrides the system-prompt parts (identity / behavior / rules)
	// from the active persona. Empty fields keep San's built-in defaults.
	Persona system.Persona

	DisabledTools map[string]bool
	MCPTools      []core.Tool

	// PermissionRules and PermissionReview are the two stages of the
	// pre-execution permission gate: the rules stage applies the static rules
	// (permit/reject/prompt); the review stage is the LLM auto-review consulted
	// only on a gray-zone prompt (AutoReview.Permission).
	PermissionRules  PermDecisionFunc
	PermissionReview PermReviewFunc
	HookEngine       hook.Handler
	AskUser          tool.AskUserFunc
	ToolProgress     func(toolCallID string, msg string)
	// BashPromptResponder answers prompts a command raises *while it runs*
	// (AutoReview.BashPrompt plus the masked secret input) — a separate concern
	// from the pre-execution gate above.
	BashPromptResponder tool.BashPromptResponderProvider

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
		system.WithPersona(p.Persona),
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
	if p.AskUser != nil {
		adaptOpts = append(adaptOpts, tool.WithAskUser(p.AskUser))
	}
	if p.ToolProgress != nil {
		adaptOpts = append(adaptOpts, tool.WithToolProgress(p.ToolProgress))
	}
	if p.BashPromptResponder != nil {
		adaptOpts = append(adaptOpts, tool.WithBashPromptResponderProvider(p.BashPromptResponder))
	}
	pb := NewPermissionBridge(p.PermissionRules)
	pb.SetReviewer(p.PermissionReview)
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
		Tools:       tool.WithPreToolUseAndPermission(tools, p.HookEngine, pb),
		CompactFunc: compactFunc,
		CWD:         p.CWD,
		OnEvent:     p.OnEvent,
	})

	return ag, pb, nil
}
