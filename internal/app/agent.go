// Agent session lifecycle: building params, delegating to *agent.Session,
// and wrapping channels in tea.Cmds for the TUI.
package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/reviewer"
	"github.com/genai-io/san/internal/session/transcript"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// ============================================================
// Build params from model state
// ============================================================

// activePersona returns the currently-selected persona, or nil for the
// built-in default (no settings.persona, or it doesn't resolve). A configured
// name that doesn't resolve logs a warning and falls back to the default.
func (m *model) activePersona() *persona.Persona {
	snap := m.services.Setting.Snapshot()
	if snap == nil || snap.Persona == "" || snap.Persona == persona.DefaultName {
		return nil
	}
	p, ok := m.services.Persona.Get(snap.Persona)
	if !ok || p.IsBuiltin() {
		log.Logger().Warn("configured persona not found; using built-in default",
			zap.String("persona", snap.Persona))
		return nil
	}
	return p
}

// personaPrompt resolves the system.Persona (prompt-part overrides) to build
// with — the active persona's three parts, or empty (the built-in default)
// when no persona is selected.
func (m *model) personaPrompt() system.Persona {
	if p := m.activePersona(); p != nil {
		return system.Persona{Identity: p.Identity, Behavior: p.Behavior, Rules: p.Rules}
	}
	return system.Persona{}
}

// applyPersonaSkills loads the active persona's bundled skills into the skill
// registry (in-memory, active by default), or clears them when no persona is
// selected. The skills-directory reminder picks the set up on its next emit.
func (m *model) applyPersonaSkills() {
	if p := m.activePersona(); p != nil {
		var states map[string]string
		if p.Settings != nil {
			states = p.Settings.Skills
		}
		m.services.Skill.LoadPersona(p.SkillDirs, states)
		return
	}
	m.services.Skill.ClearPersona()
}

// applyPersonaAgents restricts the visible subagents to the active persona's
// `agents` allow-list (empty/none = all visible), or clears the restriction
// when no persona is selected. In-memory, mirroring applyPersonaSkills.
func (m *model) applyPersonaAgents() {
	if p := m.activePersona(); p != nil && p.Settings != nil && len(p.Settings.Agents) > 0 {
		m.services.Subagent.LoadPersona(p.Settings.Agents)
		return
	}
	m.services.Subagent.ClearPersona()
}

func (m *model) buildAgentParams() agent.BuildParams {
	schemas := m.services.MCP.GetToolSchemas()
	mcpTools := mcp.AsCoreTools(schemas, mcp.NewCaller(m.services.MCP))

	maxTokens := kit.GetMaxTokens(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
	var onEvent func(core.Event)
	rec := m.services.Session.NewRecorder("main", m.env.LLMProvider.Name(), m.env.GetModelID(), maxTokens)
	if rec != nil {
		onEvent = rec.OnAgentEvent
		m.services.Hook.SetAuditCallback(func(a hook.HookFiredAudit) {
			rec.RecordHook(transcript.HookRecord{
				Event:     a.Event,
				Source:    a.Source,
				Matcher:   a.Matcher,
				Outcome:   a.Outcome,
				Reason:    a.Reason,
				LatencyMs: a.Duration.Milliseconds(),
			})
		})
		m.services.Skill.SetStateChangeObserver(func(name, previous, current, caller string) {
			rec.RecordSkillState(transcript.SkillRecord{
				Name:     name,
				Previous: previous,
				Current:  current,
				Caller:   caller,
			})
		})
	}

	var ar setting.AutoReviewSettings
	if snap := m.services.Setting.Snapshot(); snap != nil {
		ar = snap.AutoReview
	}
	reviewerProvider, reviewerModelID := m.resolveReviewerModel(ar.Model)
	rev := reviewer.New(reviewerProvider, reviewerModelID)
	if ar.SystemPromptFile != "" {
		if b, err := os.ReadFile(ar.SystemPromptFile); err == nil {
			rev.SetSystemPrompt(string(b))
		} else {
			log.Logger().Warn("auto-review systemPromptFile unreadable; using built-in rubric",
				zap.String("file", ar.SystemPromptFile), zap.Error(err))
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
		Persona:        m.personaPrompt(),

		DisabledTools: m.services.Setting.DisabledTools(),
		MCPTools:      mcpTools,
		HookEngine:    m.services.Hook,

		AskUser: func(ctx context.Context, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
			return m.conv.AgentToUI.Ask(ctx, 0, req)
		},
		ToolActivity: func(toolCallID string, msg string) {
			m.conv.AgentToUI.SendForToolCall(toolCallID, msg)
		},
		BashPromptResponder: func(ctx context.Context) tool.BashPromptResponder {
			// Interactive answering is opt-in: without it, bash stays on the
			// normal non-tty path even in auto-review.
			if !ar.AnswerBashPrompts || m.env.OperationMode != setting.ModeAutoReview {
				return nil
			}
			return bashPromptResponder{model: m, reviewer: rev}
		},

		PermissionRules: func(name string, args map[string]any) agent.PermDecisionResult {
			decision := m.services.Setting.HasPermissionToUseTool(name, args, m.env.SessionPermissions)
			mode := m.env.SessionMode()
			input := marshalPermInput(args)
			switch decision.Behavior {
			case perm.Permit:
				rec.RecordPermissionDecided(transcript.PermissionRecord{
					Tool: name, Input: input, Decision: permDecisionFor(true), Source: transcript.PermissionSourceConfig,
					Reason: decision.Reason, Mode: mode,
				})
				return agent.PermDecisionResult{Decision: decision.Behavior, Reason: decision.Reason}
			case perm.Reject:
				rec.RecordPermissionDecided(transcript.PermissionRecord{
					Tool: name, Input: input, Decision: permDecisionFor(false), Source: transcript.PermissionSourceConfig,
					Reason: decision.Reason, Mode: mode,
				})
				return agent.PermDecisionResult{Decision: decision.Behavior, Reason: decision.Reason}
			default:
				return agent.PermDecisionResult{
					Decision:    perm.Prompt,
					Reason:      decision.Reason,
					ToolName:    name,
					Description: decision.Reason,
					RequestID:   core.NewMessageID(),
					Reviewable:  decision.Reviewable,
				}
			}
		},

		PermissionReview: func(ctx context.Context, name string, args map[string]any, reason string) agent.PermReviewResult {
			// Only auto-review mode delegates gray-zone prompts to the judge.
			if m.env.OperationMode != setting.ModeAutoReview {
				return agent.PermReviewResult{}
			}
			// Defense in depth: the judge may never approve a floored action,
			// even if one somehow reaches it.
			if setting.BypassImmuneReason(name, args) != "" {
				return agent.PermReviewResult{}
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			verdict, err := rev.Permission(ctx, reviewer.Request{
				ToolName: name, Args: args, Reason: reason, CWD: m.env.CWD,
			})
			log.Logger().Debug("auto-review verdict",
				zap.String("tool", name),
				zap.Bool("allow", err == nil && verdict.Allow),
				zap.String("reason", verdict.Reason),
				zap.Error(err))
			if err != nil || !verdict.Allow {
				return agent.PermReviewResult{} // fail closed → escalate to human
			}
			if rec != nil {
				rec.RecordPermissionDecided(transcript.PermissionRecord{
					Tool: name, Input: marshalPermInput(args), Decision: permDecisionFor(true),
					Source: transcript.PermissionSourceReviewer, Reason: verdict.Reason, Mode: m.env.SessionMode(),
				})
			}
			// Cache the approval as a session grant so an identical repeat hits the
			// static fast path instead of the judge, shrinking the gray zone over
			// the session. Safe to write here: the agent goroutine is the only
			// mutator (the human-approval writer runs only while it is parked).
			m.env.SessionPermissions.AllowPattern(setting.BuildRule(name, args))
			m.reviewerApprovals.Add(1)
			return agent.PermReviewResult{Allow: true, Reason: "auto-review: " + verdict.Reason}
		},
	}
}

// resolveReviewerModel picks the provider and model for the auto-review judge.
// A "vendor/model" ref (e.g. "anthropic/claude-haiku-4-5") routes to that
// connected provider; a bare id stays on the session provider; empty uses the
// session model. An unresolvable vendor falls back to the session model.
func (m *model) resolveReviewerModel(ref string) (llm.Provider, string) {
	if ref == "" {
		return m.env.LLMProvider, m.env.GetModelID()
	}
	if vendor, id, ok := strings.Cut(ref, "/"); ok && vendor != "" && id != "" && llm.IsProvider(llm.Name(vendor)) {
		if p, err := llm.NewProviderPool(m.services.LLM.Store()).Resolve(context.Background(), llm.Name(vendor)); err == nil {
			return p, id
		}
		log.Logger().Warn("auto-review model vendor unavailable; using session model", zap.String("model", ref))
		return m.env.LLMProvider, m.env.GetModelID()
	}
	return m.env.LLMProvider, ref
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
		conv.PollPermGate(m.services.Agent.PermissionGate()),
	}
	if m.conv.AgentToUI != nil {
		cmds = append(cmds, m.conv.AgentToUI.Check())
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
	// Skill.PromptSection already produces a self-introduced body
	// ("Use the Skill tool to invoke these capabilities: ...") so it goes
	// inside <system-reminder> verbatim, matching Claude Code's shape.
	m.services.Reminder.Register(reminder.NewProvider(reminder.ProviderSkillsDirectory, func() string {
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
// Agent outbox and permission gate
// ============================================================

func (m *model) ContinueOutbox() tea.Cmd {
	if !m.services.Agent.Active() {
		return nil
	}
	return conv.DrainAgentOutbox(m.services.Agent.Outbox())
}

func (m *model) OnPermGateRequest(req *conv.PermGateRequest) tea.Cmd {
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

func (m *model) preparePermissionRequest(req *conv.PermGateRequest) *perm.PermissionRequest {
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

	executor := subagent.NewExecutor(m.env.LLMProvider, m.env.CWD, m.env.GetModelID(), m.services.Hook)
	executor.SetResolver(llm.NewProviderPool(m.services.LLM.Store()))
	if m.services.Session.GetStore() != nil && m.services.Session.ID() != "" {
		executor.SetSessionStore(m.services.Session.GetStore(), m.services.Session.ID())
	}
	executor.SetContext(m.env.IsGit)
	executor.SetCapabilities(m.services.Skill.PromptSection(), m.services.Subagent.PromptSection())
	executor.SetMCP(m.services.MCP, m.services.MCP)

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
