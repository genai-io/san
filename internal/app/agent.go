// Agent session lifecycle: building params, delegating to agent.Service,
// and wrapping channels in tea.Cmds for the TUI.
package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/agent"
	"github.com/genai-io/gen-code/internal/app/conv"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/identity"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/mcp"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/tool"
	"github.com/genai-io/gen-code/internal/tool/perm"
)

// ============================================================
// Build params from model state
// ============================================================

// activeIdentityBody returns the markdown body of the currently-active
// identity, or "" to mean "use the built-in default". Resolution order:
//  1. settings.identity (user or project level, project wins)
//  2. registry lookup by name
//  3. body field of that Identity
//
// A configured name that does not resolve logs a warning and falls back to
// the built-in default — that way a typo or stale value surfaces in the log
// instead of silently degrading the persona.
func (m *model) activeIdentityBody() string {
	if m.services.Setting == nil || m.services.Identity == nil {
		return ""
	}
	snap := m.services.Setting.Snapshot()
	if snap == nil {
		return ""
	}
	body := m.services.Identity.Active(snap.Identity)
	if body == "" && snap.Identity != "" && snap.Identity != identity.DefaultName {
		log.Logger().Warn("configured identity not found; falling back to default",
			zap.String("identity", snap.Identity))
	}
	return body
}

func (m *model) buildAgentParams() agent.BuildParams {
	var mcpTools []core.Tool
	if m.services.MCP.Registry() != nil {
		schemas := m.services.MCP.Registry().GetToolSchemas()
		mcpCaller := mcp.NewCaller(m.services.MCP.Registry())
		mcpTools = mcp.AsCoreTools(schemas, mcpCaller)
	}

	return agent.BuildParams{
		Provider:       m.env.LLMProvider,
		ModelID:        m.env.GetModelID(),
		MaxTokens:      kit.GetMaxTokens(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens),
		ThinkingEffort: m.env.EffectiveThinkingEffort(),

		CWD:     m.env.CWD,
		CWDFunc: func() string { return m.env.CWD },
		IsGit:   m.env.IsGit,

		UserInstructions:    m.env.CachedUserInstructions,
		ProjectInstructions: m.env.CachedProjectInstructions,
		SkillsPrompt:        m.services.Skill.PromptSection(),
		AgentsPrompt:        m.services.Subagent.PromptSection(),
		IdentityText:        m.activeIdentityBody(),
		SkillInvocation:     m.userInput.Skill.ActiveInvocation,

		DisabledTools: m.services.Setting.DisabledTools(),
		MCPTools:      mcpTools,

		InteractionFunc: func(ctx context.Context, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
			return m.conv.ProgressHub.Ask(ctx, 0, req)
		},
		ToolProgress: func(toolCallID string, msg string) {
			m.conv.ProgressHub.SendForToolCall(toolCallID, msg)
		},

		PermissionDecider: func(name string, args map[string]any) agent.PermDecisionResult {
			decision := m.services.Setting.HasPermissionToUseTool(name, args, m.env.SessionPermissions)
			switch decision.Behavior {
			case setting.Allow:
				return agent.PermDecisionResult{Decision: perm.Permit, Reason: decision.Reason}
			case setting.Deny:
				return agent.PermDecisionResult{Decision: perm.Reject, Reason: decision.Reason}
			default:
				return agent.PermDecisionResult{
					Decision:    perm.Prompt,
					Reason:      decision.Reason,
					ToolName:    name,
					Description: decision.Reason,
				}
			}
		},
	}
}

// ============================================================
// Agent lifecycle (delegates to services.Agent)
// ============================================================

func (m *model) ensureAgentSession() (tea.Cmd, error) {
	if m.services.Agent.Active() {
		return nil, nil
	}

	params := m.buildAgentParams()

	var coreMessages []core.Message
	if len(m.conv.Messages) > 0 {
		for _, msg := range m.conv.ConvertToProvider() {
			coreMessages = append(coreMessages, msg)
		}
	}

	if err := m.services.Agent.Start(params, coreMessages); err != nil {
		return nil, err
	}

	cmds := []tea.Cmd{
		conv.DrainAgentOutbox(m.services.Agent.Outbox()),
		conv.PollPermBridge(m.services.Agent.PermissionBridge()),
	}
	if m.conv.ProgressHub != nil {
		cmds = append(cmds, m.conv.ProgressHub.Check())
	}
	return tea.Batch(cmds...), nil
}

func (m *model) sendToAgent(content string, images []core.Image) tea.Cmd {
	if !m.services.Agent.Active() {
		return nil
	}
	svc := m.services.Agent
	return func() tea.Msg {
		svc.Send(content, images)
		return nil
	}
}

// SendToActiveAgent sends a message to the agent inbox synchronously during
// Update so it's available immediately for the agent's post-ThinkAct drain.
// The inbox channel is buffered (16), so this won't block.
func (m *model) SendToActiveAgent(content string, images []core.Image) tea.Cmd {
	if m.services.Agent.Active() {
		log.QueueLog("SendToActiveAgent: sending to inbox %q", truncate(content, 60))
		m.services.Agent.Send(content, images)
	} else {
		log.QueueLog("SendToActiveAgent: agent NOT active, message lost %q", truncate(content, 60))
	}
	return nil
}

func (m *model) StopAgentSession() {
	m.services.Agent.Stop()
}

// ============================================================
// Agent outbox and permission bridge
// ============================================================

func (m *model) ContinueOutbox() tea.Cmd {
	if !m.services.Agent.Active() {
		return nil
	}
	return conv.DrainAgentOutbox(m.services.Agent.Outbox())
}

func (m *model) HandlePermBridge(req *conv.PermBridgeRequest) tea.Cmd {
	m.services.Agent.SetPendingPermission(req)
	if req == nil {
		return nil
	}

	permReq := m.preparePermissionRequest(req)
	m.userInput.Approval.Show(permReq, m.env.Width, m.env.Height)
	return nil
}

// ============================================================
// Agent tool configuration
// ============================================================

func (m *model) preparePermissionRequest(req *conv.PermBridgeRequest) *perm.PermissionRequest {
	if resolved, ok := tool.Get(req.ToolName); ok {
		if pat, ok := resolved.(tool.PermissionAwareTool); ok {
			if rich, err := pat.PreparePermission(context.Background(), req.Input, m.env.CWD); err == nil && rich != nil {
				return rich
			}
		}
	}
	return &perm.PermissionRequest{
		ToolName:    req.ToolName,
		Description: req.Description,
	}
}

func (m *model) ReconfigureAgentTool() {
	if m.env.LLMProvider == nil {
		return
	}
	m.ensureMemoryContextLoaded()

	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook.Engine()
	}
	executor := subagent.NewExecutor(m.env.LLMProvider, m.env.CWD, m.env.GetModelID(), hookEngine)
	if m.services.Session.GetStore() != nil && m.services.Session.ID() != "" {
		executor.SetSessionStore(m.services.Session.GetStore(), m.services.Session.ID())
	}
	executor.SetContext(m.env.CachedUserInstructions, m.env.CachedProjectInstructions, m.env.IsGit)
	executor.SetCapabilities(m.services.Skill.PromptSection(), m.services.Subagent.PromptSection())
	if m.services.MCP.Registry() != nil {
		executor.SetMCP(m.services.MCP.Registry().GetToolSchemas, m.services.MCP.Registry())
	}

	adapter := subagent.NewExecutorAdapter(executor)
	type executorSetter interface{ SetExecutor(tool.AgentExecutor) }
	for _, name := range []string{tool.ToolAgent, tool.ToolSendMessage} {
		if t, ok := m.services.Tool.Get(name); ok {
			if setter, ok := t.(executorSetter); ok {
				setter.SetExecutor(adapter)
			}
		}
	}
}

// ============================================================
// LLM client
// ============================================================

func (m *model) buildLLMClient() *llm.Client {
	c := llm.NewClient(m.env.LLMProvider, m.env.GetModelID(), kit.GetMaxTokens(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens))
	c.SetThinkingEffort(m.env.EffectiveThinkingEffort())
	return c
}
