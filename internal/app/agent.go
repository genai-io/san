// Agent session lifecycle: building params, delegating to *agent.Task,
// and wrapping channels in tea.Cmds for the TUI.
package app

import (
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/zap"

	"github.com/genai-io/san/v2/internal/agent"
	"github.com/genai-io/san/v2/internal/app/conv"
	"github.com/genai-io/san/v2/internal/app/input"
	"github.com/genai-io/san/v2/internal/app/kit"
	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/core/system"
	"github.com/genai-io/san/v2/internal/hook"
	"github.com/genai-io/san/v2/internal/identity"
	"github.com/genai-io/san/v2/internal/llm"
	"github.com/genai-io/san/v2/internal/log"
	"github.com/genai-io/san/v2/internal/mcp"
	"github.com/genai-io/san/v2/internal/reminder"
	"github.com/genai-io/san/v2/internal/session/transcript"
	"github.com/genai-io/san/v2/internal/setting"
	"github.com/genai-io/san/v2/internal/subagent"
	"github.com/genai-io/san/v2/internal/tool"
	"github.com/genai-io/san/v2/internal/tool/perm"
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
	if m.services.MCP != nil {
		schemas := m.services.MCP.GetToolSchemas()
		mcpTools = mcp.AsCoreTools(schemas, mcp.NewCaller(m.services.MCP))
	}

	maxTokens := kit.GetMaxTokens(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
	var onEvent func(core.Event)
	rec := m.services.Session.NewRecorder("main", m.env.LLMProvider.Name(), m.env.GetModelID(), maxTokens)
	if rec != nil {
		onEvent = rec.OnAgentEvent
		if m.services.Hook != nil {
			if eng := m.services.Hook; eng != nil {
				eng.SetAuditCallback(func(a hook.HookFiredAudit) {
					rec.RecordHook(transcript.HookRecord{
						Event:     a.Event,
						Source:    a.Source,
						Matcher:   a.Matcher,
						Outcome:   a.Outcome,
						Reason:    a.Reason,
						LatencyMs: a.Duration.Milliseconds(),
					})
				})
			}
		}
		if m.services.Skill != nil {
			if reg := m.services.Skill; reg != nil {
				reg.SetStateChangeObserver(func(name, previous, current, caller string) {
					rec.RecordSkillState(transcript.SkillRecord{
						Name:     name,
						Previous: previous,
						Current:  current,
						Caller:   caller,
					})
				})
			}
		}
	}

	return agent.BuildParams{
		Provider:       m.env.LLMProvider,
		ModelID:        m.env.GetModelID(),
		MaxTokens:      maxTokens,
		ThinkingEffort: m.env.EffectiveThinkingEffort(),
		OnEvent:        onEvent,

		CWD:     m.env.CWD,
		CWDFunc: func() string { return m.env.CWD },
		IsGit:   m.env.IsGit,

		AgentDirectory: func() string { return m.services.Subagent.PromptSection() },
		IdentityText:   m.activeIdentityBody(),

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
			mode := m.env.SessionMode()
			input := marshalPermInput(args)
			switch decision.Behavior {
			case setting.Allow:
				rec.RecordPermissionDecided(transcript.PermissionRecord{
					Tool: name, Input: input, Decision: permDecisionFor(true), Source: transcript.PermissionSourceConfig,
					Reason: decision.Reason, Mode: mode,
				})
				return agent.PermDecisionResult{Decision: perm.Permit, Reason: decision.Reason}
			case setting.Deny:
				rec.RecordPermissionDecided(transcript.PermissionRecord{
					Tool: name, Input: input, Decision: permDecisionFor(false), Source: transcript.PermissionSourceConfig,
					Reason: decision.Reason, Mode: mode,
				})
				return agent.PermDecisionResult{Decision: perm.Reject, Reason: decision.Reason}
			default:
				return agent.PermDecisionResult{
					Decision:    perm.Prompt,
					Reason:      decision.Reason,
					ToolName:    name,
					Description: decision.Reason,
					RequestID:   core.NewMessageID(),
				}
			}
		},
	}
}

// marshalPermInput serializes the tool args for a permission audit record.
// Errors are logged but not propagated — audit must never block the decider.
func marshalPermInput(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		log.Logger().Warn("perm input marshal failed", zap.Error(err))
		return nil
	}
	return data
}

// ============================================================
// Agent lifecycle (delegates to services.Agent)
// ============================================================

// ensureAgentSession lazily starts the agent goroutine, preloading the
// existing conversation. If pendingSend is non-empty and matches the
// trailing user message in m.conv, it's dropped from the preload — the
// caller is about to re-deliver it via sendToAgent and we'd otherwise see
// the input twice. Pass "" when the caller hasn't yet appended the message.
func (m *model) ensureAgentSession(pendingSend string) (tea.Cmd, error) {
	if m.services.Agent.Active() {
		return nil, nil
	}

	params := m.buildAgentParams()

	var coreMessages []core.Message
	if len(m.conv.Messages) > 0 {
		for _, msg := range m.conv.ConvertToProvider() {
			coreMessages = append(coreMessages, msg)
		}
		if pendingSend != "" && len(coreMessages) > 0 {
			last := coreMessages[len(coreMessages)-1]
			if last.Role == core.RoleUser && last.Content == pendingSend {
				coreMessages = coreMessages[:len(coreMessages)-1]
			}
		}
	}

	if err := m.services.Agent.Start(params, coreMessages); err != nil {
		return nil, err
	}

	// Wire L1 self-learning *after* Agent.Start so the ReviewFunc can capture
	// the live Agent + System for its fork. Builds nothing if both arms are
	// off (§3.1 zero-overhead guarantee). pendingSend is forwarded so the
	// reviewer's cadence seed skips the in-flight user turn.
	m.wireSelfLearn(params, pendingSend)

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
	content = m.attachPendingReminders(content)
	return func() tea.Msg {
		svc.Send(content, images)
		return nil
	}
}

// attachPendingReminders drains the reminder queue and appends any pending
// <system-reminder> blocks to the user message content. The harness uses this
// channel to deliver session/project context (skills, memory, one-time notices)
// without invalidating the system-prompt cache prefix.
func (m *model) attachPendingReminders(content string) string {
	if m.services.Reminder == nil {
		return content
	}
	pending := m.services.Reminder.Drain()
	if len(pending) == 0 {
		return content
	}
	return reminder.AttachToContent(content, pending)
}

// wireReminderProviders registers the harness providers that emit on
// SessionStart and PostCompact. Each provider's render closure captures the
// services struct pointer so it always reads the live registry/cache state
// — that way settings reload and skill toggles surface in the next emission
// without ever mutating the cached system prompt.
func (m *model) wireReminderProviders() {
	if m.services.Reminder == nil {
		return
	}

	// Skill.PromptSection already produces a self-introduced body
	// ("Use the Skill tool to invoke these capabilities: ...") so it goes
	// inside <system-reminder> verbatim, matching Claude Code's shape.
	m.services.Reminder.Register(reminder.NewProvider(reminder.ProviderSkillsDirectory, func() string {
		if m.services.Skill == nil {
			return ""
		}
		return m.services.Skill.PromptSection()
	}))
	m.services.Reminder.Register(reminder.NewProvider(reminder.ProviderMemoryUser, func() string {
		return reminder.WrapMemory("user", m.env.CachedUserInstructions)
	}))
	m.services.Reminder.Register(reminder.NewProvider(reminder.ProviderMemoryProject, func() string {
		return reminder.WrapMemory("project", m.env.CachedProjectInstructions)
	}))
	// Agent-written auto-memory (L1 reviewer's store). Read at Render() time so
	// PostCompact / cwd change picks up the latest written entries without a
	// separate refresh hook (see notes/active/l1-background-review.md §4.5).
	// Kept as its own scope so agent-written entries never mix with the
	// user-authored memory above.
	m.services.Reminder.Register(reminder.NewProvider(reminder.ProviderMemoryAuto, func() string {
		body, ok := system.LoadAutoMemory(m.env.CWD)
		if !ok {
			return ""
		}
		return reminder.WrapMemory("auto", body)
	}))
}

func (m *model) StopAgentSession() {
	m.services.Agent.Stop()
	// Stop feeding the L1 reviewer AND cancel the session-scoped context
	// so an in-flight fork unblocks immediately instead of holding tokens /
	// HTTP for up to forkDeadline.
	m.teardownSelfLearn()
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

func (m *model) OnPermBridgeRequest(req *conv.PermBridgeRequest) tea.Cmd {
	m.services.Agent.SetPendingPermission(req)
	if req == nil {
		return nil
	}

	permReq := m.preparePermissionRequest(req)
	// Emit permission.required with the metadata about to be rendered to the
	// user — that way the audit captures the same context the user saw.
	if rec := m.services.Session.Recorder(); rec != nil {
		rec.RecordPermissionRequired(transcript.PermissionRecord{
			RequestID:      req.RequestID,
			Tool:           req.ToolName,
			Input:          marshalPermInput(req.Input),
			Detail:         permDetail(permReq),
			OptionsOffered: input.BuildApprovalOptions(permReq),
			Source:         transcript.PermissionSourceAsk,
			Mode:           m.env.SessionMode(),
		})
	}
	m.userInput.Approval.Show(permReq, m.env.Width, m.env.Height)
	return nil
}

// permDetail serializes the *derived* permission context — fields the
// resolver computed or looked up beyond the raw tool args. Anything that is
// already a verbatim echo of req.Input (Bash command/description, Skill
// args/name, the file_path that auditors can read straight from input) is
// stripped so the audit record doesn't double-store the same values.
func permDetail(req *perm.PermissionRequest) json.RawMessage {
	if req == nil {
		return nil
	}
	var payload any
	switch {
	case req.SkillMeta != nil:
		m := req.SkillMeta
		payload = struct {
			Description string   `json:"description,omitempty"`
			ScriptCount int      `json:"scriptCount,omitempty"`
			RefCount    int      `json:"refCount,omitempty"`
			Scripts     []string `json:"scripts,omitempty"`
			References  []string `json:"references,omitempty"`
		}{m.Description, m.ScriptCount, m.RefCount, m.Scripts, m.References}
	case req.BashMeta != nil:
		payload = struct {
			LineCount int `json:"lineCount,omitempty"`
		}{req.BashMeta.LineCount}
	case req.AgentMeta != nil:
		payload = req.AgentMeta
	case req.DiffMeta != nil:
		payload = req.DiffMeta
	default:
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Logger().Warn("perm detail marshal failed", zap.Error(err))
		return nil
	}
	return data
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
		hookEngine = m.services.Hook
	}
	executor := subagent.NewExecutor(m.env.LLMProvider, m.env.CWD, m.env.GetModelID(), hookEngine)
	if m.services.Session.GetStore() != nil && m.services.Session.ID() != "" {
		executor.SetSessionStore(m.services.Session.GetStore(), m.services.Session.ID())
	}
	executor.SetContext(m.env.IsGit)
	executor.SetCapabilities(m.services.Skill.PromptSection(), m.services.Subagent.PromptSection())
	if m.services.MCP != nil {
		executor.SetMCP(m.services.MCP, m.services.MCP)
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
