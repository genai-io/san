// Agent session lifecycle: building params, delegating to *agentruntime.Runtime,
// and wrapping channels in tea.Cmds for the TUI.
package tui

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/ask"
	apphook "github.com/genai-io/san/internal/app/hook"
	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/infra/confdir"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	agentruntime "github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/session"
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
	snap := m.features.Setting.Snapshot()
	if snap == nil || snap.Persona == "" || snap.Persona == persona.DefaultName {
		return nil
	}
	p, ok := m.features.Persona.Get(snap.Persona)
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
		m.features.Skill.LoadPersona(p.SkillDirs, states)
		return
	}
	m.features.Skill.ClearPersona()
}

// applyPersonaAgents restricts the visible subagents to the active persona's
// `agents` allow-list (empty/none = all visible), or clears the restriction
// when no persona is selected. In-memory, mirroring applyPersonaSkills.
func (m *model) applyPersonaAgents() {
	if p := m.activePersona(); p != nil && p.Settings != nil && len(p.Settings.Agents) > 0 {
		m.features.Subagent.LoadPersona(p.Settings.Agents)
		return
	}
	m.features.Subagent.ClearPersona()
}

// promptParams fills in the fields that shape the system prompt and the
// toolset, and only those. It is free of side effects, unlike buildAgentParams
// below, which layers the writer, hook wiring, and permission closures on
// top — so anything that only wants to know what the agent would send (see
// contextUsage) can call this without starting anything.
//
// Both callers going through one constructor is what keeps them in step: a new
// prompt-shaping input added here reaches the inspector for free, where a
// second hand-written literal would silently under-report.
func (m *model) promptParams() agentruntime.BuildParams {
	return agentruntime.BuildParams{
		CWD:     m.env.CWD,
		Persona: m.personaPrompt(),
		AgentCatalog: func() string {
			if m.features.Subagent == nil {
				return ""
			}
			return m.features.Subagent.AgentCatalog()
		},
		DisabledTools: m.disabledTools(),
		MCPTools:      mcp.AsCoreTools(m.features.MCP.ToolSchemas(), m.features.MCP),

		// Inject the ReviewSkill trigger tool (tailored to the enabled capabilities)
		// so the model can queue its own self-learning reviews.
		ExtraTools: m.autoSkillExtraTools(),

		AutoMemory:  m.currentAutoMemory(),
		Skills:      m.features.Skill,
		GroupRoster: group.Roster,
	}
}

func (m *model) buildAgentParams() (agentruntime.BuildParams, error) {
	if m.features.Setting == nil || m.env.SessionPermissions == nil {
		return agentruntime.BuildParams{}, fmt.Errorf("main-session runtime requires a permission policy")
	}
	params := m.promptParams()

	rec, err := m.openCurrentSession()
	if err != nil {
		return params, err
	}
	owner := agentruntime.MainSession{Session: rec, Store: m.features.SessionStore}
	if err := owner.Validate(); err != nil {
		return params, err
	}
	for _, n := range m.earlyNotices {
		if err := rec.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: n.Content, ByAgent: n.AgentNotice}}); err != nil {
			return params, err
		}
	}
	m.earlyNotices = nil

	// Build the autopilot judge into the atomic slot so /autopilot Save can
	// hot-swap it mid-session; the closures below Load it per call.
	m.rebuildAutopilotJudge()

	// Stream timeout overrides. Priority: SAN_* env var > settings.json > core
	// default (zero value => core picks FirstChunkTimeout=5m, IdleTimeout=60s).
	streamFirstChunk := firstValidDuration(
		setting.Getenv("STREAM_FIRST_CHUNK_TIMEOUT"),
		m.features.Setting.StreamFirstChunkTimeout(),
	)
	streamIdle := firstValidDuration(
		setting.Getenv("STREAM_IDLE_TIMEOUT"),
		m.features.Setting.StreamIdleTimeout(),
	)

	params.Provider = m.env.LLMProvider
	params.Recording = rec
	params.ModelID = m.env.GetModelID()
	params.MaxTokens = m.sessionMaxOutput()
	params.ThinkingEffort = m.env.EffectiveThinkingEffort()
	params.FileCache = m.env.FileCache
	params.Tracker = m.features.Tracker
	params.CWDFunc = func() string { return m.autopilot.Load().cwd }

	bridge := m.conv.AgentToUI
	params.AskUser = func(ctx context.Context, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
		return bridge.Ask(ctx, tool.AgentInteractionSource{}, req)
	}
	params.ToolActivity = bridge.SendForToolCall
	promptOwner, promptSessionID := params.Recording, rec.ID()
	params.BashPromptResponder = func(ctx context.Context) tool.BashPromptResponder {
		// Interactive answering is opt-in via the Bash steer, read live (via
		// the synchronized snapshot — this runs on the agent goroutine) so a
		// mid-session toggle takes effect: without it, bash stays on the
		// normal non-tty path even in autopilot.
		if !m.autopilotEngaged() || !m.liveAutopilotConfig().Steers.BashPrompt {
			return nil
		}
		return bashPromptResponder{model: m, bridge: bridge, recording: promptOwner, sessionID: promptSessionID}
	}

	params.StreamFirstChunkTimeout = streamFirstChunk
	params.StreamIdleTimeout = streamIdle
	// Bind extension audits here, where the application owns their lifecycle.
	// Session validation and agent construction never rebind shared services.
	params.SessionID = rec.ID()
	providerName := ""
	if params.Provider != nil {
		providerName = params.Provider.Name()
	}
	recorder := session.NewRecorder(rec, providerName)
	if engine := m.features.Hook; engine != nil {
		engine.SetSession(rec.ID(), rec.Path())
		apphook.Bind(engine, params.Provider, rec, params.ModelID)
		params.HookEngine = engine
	}
	if skills := m.features.Skill; skills != nil {
		skills.SetStateChangeObserver(recorder.RecordSkillStateChange)
	}
	policy := agentruntime.PermissionPolicy{
		Settings: m.features.Setting, Session: m.env.SessionPermissions,
	}
	params.PermissionRules = policy.Decide
	params.CanApproveByHook = policy.CanApproveByHook
	params.PermissionJudge = func(ctx context.Context, name string, args map[string]any, reason string) agentruntime.PermJudgeResult {
		rt := m.autopilot.Load()
		if rt == nil {
			return agentruntime.PermJudgeResult{}
		}
		return policy.Judge(ctx, name, args, reason, agentruntime.JudgeSnapshot{
			Engaged: m.autopilotEngaged(), Enabled: rt.cfg.Steers.PermissionOn(),
			CWD: rt.cwd, UnderGit: rt.underGit, Mission: rt.cfg.ActiveMission(),
			Judge: rt.judge.Permission, Observe: m.recordDecision, Recording: rec,
		})
	}
	params.ResultFilter = agentruntime.ResultLimiter{Store: owner.Store, SessionID: owner.Session.ID()}.Filter
	return params, nil
}

// resolveJudgeModel picks the provider and model for the autopilot judge.
func (m *model) resolveJudgeModel(ref string) (llm.Provider, string) {
	p, info := m.resolveJudge(ref)
	return p, info.ModelID
}

// resolveJudge resolves a judge model ref to its provider and model. A
// "vendor/model" ref (e.g. "anthropic/claude-haiku-4-5") routes to that
// connected provider; a bare id stays on the session provider; empty uses the
// session model. An unresolvable vendor falls back to the session model. The
// info names whichever provider was actually picked, for capability lookups.
func (m *model) resolveJudge(ref string) (llm.Provider, *llm.CurrentModelInfo) {
	session := &llm.CurrentModelInfo{ModelID: m.env.GetModelID()}
	if m.env.CurrentModel != nil {
		session.Provider, session.AuthMethod = m.env.CurrentModel.Provider, m.env.CurrentModel.AuthMethod
	}
	if ref == "" {
		return m.env.LLMProvider, session
	}
	if vendor, id, ok := llm.ParseVendorModel(ref); ok {
		if p, err := llm.NewProviderPool(m.features.LLM.Store()).Resolve(context.Background(), vendor); err == nil {
			return p, &llm.CurrentModelInfo{ModelID: id, Provider: vendor}
		}
		log.Logger().Warn("judge model vendor unavailable; using session model", zap.String("model", ref))
		return m.env.LLMProvider, session
	}
	session.ModelID = ref
	return m.env.LLMProvider, session
}

// autopilotModelEfforts lists the reasoning rungs a "vendor/model" ref offers,
// or nil for a ref that cannot be resolved or a model that does not reason.
func (m *model) autopilotModelEfforts(ref string) []string {
	if _, _, ok := llm.ParseVendorModel(ref); !ok {
		return nil
	}
	p, info := m.resolveJudge(ref)
	return llm.ThinkingEffortsForModel(p, m.features.LLM.Store(), info)
}

// autopilotModelRefs lists "vendor/model" refs for the /autopilot model picker:
// the cached models of each connected provider under its connected auth method.
func (m *model) autopilotModelRefs() []string {
	store := m.features.LLM.Store()
	if store == nil {
		return nil
	}
	cached := store.CachedModelsByProvider()
	var refs []string
	for vendor, conn := range store.Connections() {
		for _, mdl := range cached[vendor+":"+string(conn.AuthMethod)] {
			refs = append(refs, vendor+"/"+mdl.ID)
		}
	}
	slices.Sort(refs)
	return refs
}

// recordDecision tallies one judge decision for the status-bar count and
// stashes it for the renderer, keyed by the tool call ID carried in ctx and
// consumed (load + delete) in TakeJudgeDecision — so the handoff map only ever
// holds calls whose result has not arrived yet. The approved flag
// picks the counter, so the count and the inline annotations can never drift.
// The count is bumped even when ctx carries no tool call ID; only the stash
// needs one.
func (m *model) recordDecision(ctx context.Context, approved bool, reason string) {
	if approved {
		m.judgeApprovals.Add(1)
	} else {
		m.judgeEscalations.Add(1)
	}
	if id := core.ToolCallIDFromContext(ctx); id != "" {
		m.pendingDecisions.Store(id, core.JudgeDecision{Approved: approved, Reason: reason})
	}
}

// TakeJudgeDecision consumes the decision stashed for a tool call (load + delete),
// so it stamps exactly one rendered result and the handoff map holds only
// in-flight calls. Returns nil when the call was not judged. Mirrors the
// tool side-effect handoff (tool.PopSideEffect), consumed at the same point in
// OnToolResult.
func (m *model) TakeJudgeDecision(callID string) *core.JudgeDecision {
	// No empty-callID guard: recordDecision never stores under an empty key, so
	// LoadAndDelete("") already misses and returns nil.
	v, ok := m.pendingDecisions.LoadAndDelete(callID)
	if !ok {
		return nil
	}
	decision := v.(core.JudgeDecision)
	return &decision
}

// firstValidDuration returns the first value that parses as a time.Duration
// (e.g. "30s", "3m"), in priority order. Empty/invalid values are skipped;
// zero means "no override" and lets the core default apply.
func firstValidDuration(vals ...string) time.Duration {
	for _, v := range vals {
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0
}

// ============================================================
// Agent lifecycle (delegates to services.Agent)
// ============================================================

// disabledTools respects configured worker-messaging disables. Group sessions
// additionally hold SendMessage on so their members remain reachable.
func (m *model) disabledTools() map[string]bool {
	disabled := maps.Clone(m.features.Setting.DisabledTools())
	for name := range toolsHeldOn() {
		delete(disabled, name)
	}
	return disabled
}

// toolsHeldOn names the tools this session keeps on whatever the settings
// say, and why: the runtime layer over the configured one, read by the agent
// build and shown in /tool.
func toolsHeldOn() map[string]string {
	if g, _ := group.Current(); g != "" {
		return map[string]string{
			tool.ToolSendMessage: "on while in a group",
		}
	}
	return nil
}

// disabledToolsSignature is a stable fingerprint of the effective disabled-tool
// set. ensureAgentRuntime compares it against the live agent's build-time value
// so a /tool enable/disable (which the built-in and MCP tool filters both read)
// rebuilds the toolset on the next turn.
func (m *model) disabledToolsSignature() string {
	disabled := m.disabledTools()
	names := make([]string, 0, len(disabled))
	for name, off := range disabled {
		if off {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, "\x00")
}

// ensureAgentRuntime starts the agent with its retained native conversation.
func (m *model) ensureAgentRuntime() (tea.Cmd, error) {
	if m.features.Agent.Active() {
		// The toolset is fixed at agent-build time. If the enabled ReviewSkill
		// capabilities or the disabled-tool set drifted since the build (a
		// /memory save, a /tool toggle, an external settings edit), stop the
		// stale session and fall through to rebuild with the current toolset —
		// the one choke point every turn passes through, so all drift sources
		// are covered uniformly.
		if !m.agentReloadPending && m.autoSkillCapabilities() == m.agentReviewCaps &&
			m.disabledToolsSignature() == m.agentDisabledToolsSignature &&
			m.currentAutoMemory() == m.autoMemory {
			return nil, nil
		}
		if err := m.StopAgentRuntime(); err != nil {
			return nil, err
		}
	}

	if _, err := m.openCurrentSession(); err != nil {
		return nil, err
	}
	// Rebuild the subagent executor alongside the main agent so it inherits the
	// same effective disabled-tool snapshot after settings/persona drift.
	m.ReconfigureAgentTool()
	params, err := m.buildAgentParams()
	if err != nil {
		return nil, err
	}
	if w := m.features.Session; w != nil && w.Err() != nil {
		return nil, w.Err()
	}
	if err := m.cancelManualCompaction(); err != nil {
		return nil, err
	}
	projection := m.features.Session.Snapshot()
	if len(projection.ActiveMessages())+len(projection.Instructions())+len(projection.Tools())+len(projection.Skills()) > 0 {
		_, err = m.features.Agent.Resume(params)
	} else {
		err = m.features.Agent.Start(params, nil)
	}
	if err != nil {
		return nil, err
	}
	m.agentSession = m.features.Session
	m.agentReloadPending = false
	// Record what the toolset was built with, for the drift check above.
	m.agentReviewCaps = m.autoSkillCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	m.autoMemory = params.AutoMemory
	m.conv.AutoMemoryStore = params.AutoMemory.Store

	// Bind Auto Skill to the running session.
	m.wireAutoSkill(params)

	cmds := []tea.Cmd{
		chat.DrainAgentOutbox(m.features.Agent.Outbox()),
		chat.PollPermGate(m.features.Agent.PermissionGate()),
	}
	return tea.Batch(cmds...), nil
}

// agentSendFailedMsg reports a message the agent never accepted; a delivered
// message needs no round trip.
type agentSendFailedMsg struct{ err error }

// Defer rebuild until OnTurnEnd acknowledges its consumer receipt.
type pendingInputReadyMsg struct{ outbox <-chan core.Event }

type pendingMessage = agentruntime.PendingInput

func (m *model) sendToAgent(p pendingMessage) tea.Cmd {
	if m.deferFlowMainInput(p) {
		return nil
	}
	m.pendingStopHook = nil
	m.pendingMessages.Enqueue(p)
	return m.sendPendingMessages()
}

func (m *model) discardPendingMessages() {
	m.cancelMemoryInput()
	if err := m.pendingMessages.Discard(); err != nil {
		log.Logger().Error("discard pending input recording", zap.Error(err))
		m.conv.AddNotice("Pending input could not be recorded: " + err.Error())
	}
}

func (m *model) sendPendingMessages() tea.Cmd {
	return m.sendPendingMessagesSince(time.Time{})
}

func (m *model) sendPendingMessagesSince(startedAt time.Time) tea.Cmd {
	if m.flowMainBlocksQueue() {
		return nil
	}
	if !m.pendingMessages.CanDeliver(false) || m.pendingMessages.Len() == 0 {
		return nil
	}
	if !m.features.Agent.Active() {
		return nil
	}
	if !m.conv.Stream.Active && m.agentReloadPending {
		outbox := m.features.Agent.Outbox()
		return func() tea.Msg { return pendingInputReadyMsg{outbox: outbox} }
	}
	if m.memoryInput != nil {
		return nil
	}
	var cmds []tea.Cmd
	for m.pendingMessages.Len() > 0 {
		head := m.pendingMessages.First()
		if m.needsMemoryInput(head) {
			return tea.Batch(append(cmds, m.prepareMemoryInput(head))...)
		}
		if !m.conv.Stream.Active {
			m.turnOpenedBy = head.ID
		}
		if m.pendingPluginRoot != "" {
			m.features.Agent.SetPluginRoot(m.pendingPluginRoot)
			m.pendingPluginRoot = ""
		}
		if err := m.pendingMessages.DeliverFirst(m.features.Agent.SendInput, m.attachPendingReminders); err != nil {
			return tea.Batch(append(cmds, func() tea.Msg { return agentSendFailedMsg{err: err} })...)
		}
		if !m.conv.Stream.Active {
			if startedAt.IsZero() {
				startedAt = time.Now()
			}
			m.conv.Stream = chat.StreamState{Active: true, Waiting: true, StartedAt: startedAt}
			cmds = append(cmds, m.conv.Spinner.Tick)
		}
	}
	return tea.Batch(cmds...)
}

// attachPendingReminders drains the reminder queue and appends any pending
// <system-reminder> blocks to the user message content. The harness uses this
// channel to deliver session/project context (skills, memory, one-time notices)
// without invalidating the system-prompt cache prefix.
func (m *model) attachPendingReminders(msg core.Message) core.Message {
	// Reminders (the roster among them) precede held member messages.
	return m.attachWaitingMessages(m.features.Reminder.PrepareInput(msg))
}

// wireReminderProviders registers the harness providers attached once per
// conversation. Each provider's render closure
// captures the appFeatures struct pointer so it always reads the live state
// — that way settings reload and skill toggles surface in the next emission
// without ever mutating the cached system prompt.
func (m *model) wireReminderProviders() {
	// Skill.PromptSection already produces a self-introduced body
	// ("Use the Skill tool to invoke these capabilities: ...") so it goes
	// inside <system-reminder> verbatim, matching Claude Code's shape.
	m.features.Reminder.Register(reminder.ProviderSkillsDirectory, func() string {
		return m.features.Skill.PromptSection()
	})
	m.features.Reminder.Register(reminder.ProviderMemoryUser, func() string {
		return m.env.CachedUserInstructions
	})
	m.features.Reminder.Register(reminder.ProviderMemoryProject, func() string {
		return m.env.CachedProjectInstructions
	})
	m.features.Reminder.Register(reminder.ProviderGroup, group.Roster)
}

const agentStopTimeout = 2 * time.Second

func (m *model) StopAgentRuntime() error {
	m.cancelMemoryInput()
	m.cancelAutopilot()
	if err := m.cancelManualCompaction(); err != nil {
		return err
	}
	m.pendingStopHook = nil
	ctx, cancel := context.WithTimeout(context.Background(), agentStopTimeout)
	defer cancel()
	if err := m.features.Agent.Stop(ctx); err != nil {
		log.Logger().Warn("agent runtime shutdown incomplete", zap.Error(err))
		return fmt.Errorf("stop agent runtime: %w", err)
	}
	m.teardownAutoSkill()
	return nil
}

// ResetAgentRuntime stops the agent and clears its conversation and UI state.
func (m *model) ResetAgentRuntime() error {
	m.cancelMemoryInput()
	m.cancelAutopilot()
	if err := m.cancelManualCompaction(); err != nil {
		return err
	}
	m.pendingStopHook = nil
	ctx, cancel := context.WithTimeout(context.Background(), agentStopTimeout)
	defer cancel()
	if err := m.features.Agent.Reset(ctx); err != nil {
		log.Logger().Warn("agent runtime reset incomplete", zap.Error(err))
		return fmt.Errorf("reset agent runtime: %w", err)
	}
	m.pendingMessages.ResetAdmission()
	m.pendingPluginRoot = ""
	m.resetUserPrompts()
	// The next conversation carries neither the old notices nor, until its
	// first message, the skills/memory reminders.
	m.features.Reminder.ResetConversation()
	// Compaction must not restore the old conversation's files.
	m.env.FileCache.Clear()
	// Cancel any review owned by the discarded conversation.
	m.teardownAutoSkill()
	return nil
}

// ============================================================
// Agent outbox and permission gate
// ============================================================

func (m *model) AgentOutbox() <-chan core.Event { return m.features.Agent.Outbox() }

func (m *model) ListenForAgentEvents() tea.Cmd {
	outbox := m.features.Agent.Outbox()
	if outbox == nil {
		return nil
	}
	return chat.DrainAgentOutbox(outbox)
}

func (m *model) HandlePermGate(req *agentruntime.PermGateRequest) tea.Cmd {
	if req == nil || (req.Context != nil && req.Context.Err() != nil) {
		return chat.PollPermGate(m.features.Agent.PermissionGate())
	}
	permissionView, err := m.preparePermissionRequest(req)
	if err != nil {
		// Preparation failures are tool errors, not requests for human consent.
		// The gate owns a buffered response channel; never enqueue an invalid
		// call or let a generic preview hide the actionable validation reason.
		select {
		case req.Response <- agentruntime.PermGateResponse{Reason: fmt.Sprintf("cannot prepare %s approval: %v", req.ToolName, err)}:
		default:
		}
		return chat.PollPermGate(m.features.Agent.PermissionGate())
	}
	return tea.Batch(m.enqueueUserPrompt(&chat.UserPromptRequest{
		Context: req.Context, Owner: m.conv.AgentToUI, Permission: req, PermissionView: permissionView,
		Source: tool.AgentInteractionSource{WorkingDirectory: m.env.CWD, PermissionMode: m.env.SessionMode()},
	}), chat.PollPermGate(m.features.Agent.PermissionGate()))
}

// ============================================================
// Agent tool configuration
// ============================================================

func (m *model) preparePermissionRequest(req *agentruntime.PermGateRequest) (*perm.PermissionRequest, error) {
	return ask.PreparePermission(req, m.env.CWD, m.env.SessionMode(), m.features.Setting, m.env.SessionPermissions)
}

// configureFlowPaths points Flow at the project definitions, then the user's.
func (m *model) configureFlowPaths() {
	if m.features.Flow == nil {
		return
	}
	dirs := []string{flow.DefinitionDir(confdir.Dir(m.env.CWD))}
	if home, _ := os.UserHomeDir(); home != "" {
		dirs = append(dirs, flow.DefinitionDir(confdir.Dir(home)))
	}
	m.features.Flow.SetSearchPaths(dirs)
}

func (m *model) ReconfigureAgentTool() {
	m.configureFlowPaths()
	if m.env.LLMProvider == nil || m.features.Session == nil {
		return
	}
	m.ensureMemoryContextLoaded()

	executor := subagent.NewExecutor(m.env.LLMProvider, m.env.CWD, m.env.GetModelID(), m.features.Hook)
	executor.SetInteractions(m.conv.AgentToUI)
	executor.SetDefaultModel(m.features.Setting.SubagentModel())
	executor.SetResolver(llm.NewProviderPool(m.features.LLM.Store()))
	perms := m.env.SessionPermissions
	executor.SetParentPermissionMode(func() subagent.PermissionMode {
		return subagent.InheritedPermissionMode(perms.CurrentMode())
	})
	if err := executor.SetSessions(m.features.SessionStore, m.features.Session); err != nil {
		m.conv.AddNotice("Cannot configure agents: " + err.Error())
		return
	}
	settings, bridge := m.features.Setting, m.conv.AgentToUI
	executor.ConfigureOwner(m.features.Task, m.features.Subagent, func(child *session.Session, _ bool, source tool.AgentInteractionSource) tool.Resolver {
		return newWorkerResolver(settings, perms, bridge, child, source)
	}, nil)
	executor.SetTracker(m.features.Tracker)
	parentHooks := m.features.Hook
	executor.SetChildHooks(func(owner *session.Session, provider llm.Provider, model string) (hook.Handler, func(llm.Provider, string), func()) {
		return apphook.BindChild(apphook.ForChild(parentHooks, owner), provider, owner, model)
	})
	files := m.env.FileCache
	executor.SetPostTool(func(handler hook.Handler, result core.ToolResult, effect any) error {
		engine, _ := handler.(*hook.Engine)
		agentruntime.PostTool(result, effect, engine, agentruntime.WorkspaceCallbacks{TouchFile: files.Touch})
		return nil
	})
	executor.SetProjectInstructions(m.env.CachedProjectInstructions)
	executor.SetSkillsDirectory(m.features.Skill.PromptSection())
	executor.SetMCPDependencies(m.features.MCP)
	executor.SetDisabledTools(m.features.Setting.DisabledTools())
	executor.SetParentTools(m.features.Agent.ParentToolSource(m.features.Session, m.promptParams().ToolSchemas()))

	adapter := subagent.NewExecutorAdapter(executor)
	type executorSetter interface{ SetExecutor(tool.AgentExecutor) }
	for _, name := range []string{tool.ToolAgent, tool.ToolSendMessage} {
		t, ok := m.features.Tool.Get(name)
		if !ok {
			continue
		}
		if setter, ok := t.(executorSetter); ok {
			setter.SetExecutor(adapter)
		}
	}
	if m.features.Flow != nil {
		// Use the same session-owned native executor as ordinary agent tools.
		m.features.Flow.SetExecutor(adapter)
		if m.flowMain == nil {
			m.flowMain = newFlowMainBridge()
		}
		m.features.Flow.SetMainExecutor(m.flowMain.bind(m.features.Session))
	}
}

// ============================================================
// LLM client
// ============================================================

func (m *model) buildLLMClient() *llm.Client {
	c := llm.NewClient(m.env.LLMProvider, m.env.GetModelID(), m.sessionMaxOutput())
	c.SetThinkingEffort(m.env.EffectiveThinkingEffort())
	return c
}
