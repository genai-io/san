// Agent session lifecycle: building params, delegating to *agent.Runtime,
// and wrapping channels in tea.Cmds for the TUI.
package app

import (
	"github.com/genai-io/sdk-go/pkg/ai"

	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/reviewer"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
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

// promptParams fills in the fields that shape the system prompt and the
// toolset, and only those. It is free of side effects, unlike buildAgentParams
// below, which layers the writer, hook wiring, and permission closures on
// top — so anything that only wants to know what the agent would send (see
// contextUsage) can call this without starting anything.
//
// Both callers going through one constructor is what keeps them in step: a new
// prompt-shaping input added here reaches the inspector for free, where a
// second hand-written literal would silently under-report.
func (m *model) promptParams() agent.BuildParams {
	return agent.BuildParams{
		CWD:     m.env.CWD,
		Persona: m.personaPrompt(),
		AgentCatalog: func() string {
			if m.services.Subagent == nil {
				return ""
			}
			return m.services.Subagent.AgentCatalog()
		},
		DisabledTools: m.disabledTools(),
		MCPTools:      mcp.AsCoreTools(m.services.MCP.ToolSchemas(), m.services.MCP),

		// Inject the Evolve trigger tool (tailored to the enabled capabilities)
		// so the model can queue its own self-learning reviews.
		ExtraTools: m.selfLearnExtraTools(),

		AutoMemory: m.autoMemory(),
	}
}

// syncMCPTools replaces the mcp__* tools in the running agent's registry with
// what the servers now advertise. A server that loads tools lazily, or gates
// them behind a login, changes its list while connected, and the registry is
// what the loop reads at every step — so the next step sees the change without
// the agent being rebuilt.
//
// It runs on the MCP session's own goroutine, which is why it touches only
// services: core.Tools locks, and so do Session.Tools and DisabledTools.
func (m *model) syncMCPTools() {
	tools := m.services.Agent.Tools()
	if tools == nil {
		return // no agent yet — whatever it is built with will be current
	}
	disabled := m.services.Setting.DisabledTools()

	live := make(map[string]bool)
	for _, t := range mcp.AsCoreTools(m.services.MCP.ToolSchemas(), m.services.MCP) {
		name := t.Schema().Name
		// The /tool panel's disable applies to MCP tools here for the same
		// reason agent.BuildParams applies it when the agent is built.
		if disabled[name] {
			continue
		}
		live[name] = true
		tools.Add(t, "mcp:"+name)
	}
	for _, schema := range tools.Schemas() {
		if mcp.IsMCPTool(schema.Name) && !live[schema.Name] {
			tools.Remove(schema.Name, "mcp:"+schema.Name)
		}
	}
}

func (m *model) buildAgentParams() (agent.BuildParams, error) {
	params := m.promptParams()

	maxTokens := kit.GetMaxOutput(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens)
	var onEvent func(core.Event)
	rec, err := m.openCurrentSession()
	if err != nil {
		return params, err
	}
	if rec != nil {
		onEvent = rec.RecordEvent
		for _, n := range m.earlyNotices {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded,
				Notice: &transcript.NoticeRecord{Text: n.Content, Agent: n.AgentNotice,
					AfterMessageID: conv.MemoryRecallNoticeAnchor(n.Content)}})
		}
		m.earlyNotices = nil
		m.services.Hook.SetAuditCallback(func(a hook.HookFiredAudit) {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.HookFired, Hook: &transcript.HookRecord{
				Event:     a.Event,
				Source:    a.Source,
				Matcher:   a.Matcher,
				Outcome:   a.Outcome,
				Reason:    a.Reason,
				LatencyMs: a.Duration.Milliseconds(),
			}})
		})
		m.services.Skill.SetStateChangeObserver(func(name, previous, current, caller string) {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.SkillStateChanged, Skill: &transcript.SkillRecord{
				Name:     name,
				Previous: previous,
				Current:  current,
				Caller:   caller,
			}})
		})
	}

	// Build the autopilot judge into the atomic slot so /autopilot Save can
	// hot-swap it mid-session; the closures below Load it per call.
	m.rebuildAutopilotReviewer()

	// Stream timeout overrides. Priority: SAN_* env var > settings.json > core
	// default (zero value => core picks FirstChunkTimeout=5m, IdleTimeout=60s).
	streamFirstChunk := firstValidDuration(
		setting.Getenv("STREAM_FIRST_CHUNK_TIMEOUT"),
		m.services.Setting.StreamFirstChunkTimeout(),
	)
	streamIdle := firstValidDuration(
		setting.Getenv("STREAM_IDLE_TIMEOUT"),
		m.services.Setting.StreamIdleTimeout(),
	)

	params.Provider = m.env.LLMProvider
	params.SessionID = rec.ID()
	params.ModelID = m.env.GetModelID()
	params.MaxTokens = maxTokens
	params.ThinkingEffort = m.env.EffectiveThinkingEffort()
	params.OnEvent = onEvent
	params.FileCache = m.env.FileCache
	params.CWDFunc = func() string { return m.env.CWD }
	params.HookEngine = m.services.Hook

	bridge := m.conv.AgentToUI
	params.AskUser = func(ctx context.Context, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
		return bridge.Ask(ctx, tool.AgentInteractionSource{}, req)
	}
	params.ToolActivity = bridge.SendForToolCall
	params.BashPromptResponder = func(ctx context.Context) tool.BashPromptResponder {
		// Interactive answering is opt-in via the Bash steer, read live (via
		// the synchronized snapshot — this runs on the agent goroutine) so a
		// mid-session toggle takes effect: without it, bash stays on the
		// normal non-tty path even in auto-review.
		if !m.autopilotEngaged() || !m.liveAutopilotConfig().Steers.BashPrompt {
			return nil
		}
		return bashPromptResponder{model: m, bridge: bridge}
	}

	if m.memoryRecall == nil {
		m.memoryRecall = &memoryRecall{surfaced: map[string]bool{}}
	}
	params.ResultFilter = agent.WithRecall(m.filterOversizedResult, m.memoryRecall.current.Load)
	params.PermissionRules = func(name string, args map[string]any) agent.PermDecisionResult {
		var decision setting.PermissionDecision
		if permitAutoMemoryWrite(params.AutoMemory, name, args, m.services.Setting.DenyRule) {
			decision = setting.PermissionDecision{Behavior: perm.Permit, Reason: "auto memory"}
		} else {
			decision = m.services.Setting.HasPermissionToUseTool(name, args, m.env.SessionPermissions)
		}
		mode := m.env.SessionMode()
		input := marshalPermInput(args)
		switch decision.Behavior {
		case perm.Permit:
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
				Tool: name, Input: input, Decision: permDecisionFor(true), Source: transcript.PermissionSourceConfig,
				Reason: decision.Reason, Mode: mode,
			}})
			return agent.PermDecisionResult{Decision: decision.Behavior, Reason: decision.Reason}
		case perm.Reject:
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
				Tool: name, Input: input, Decision: permDecisionFor(false), Source: transcript.PermissionSourceConfig,
				Reason: decision.Reason, Mode: mode,
			}})
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
	}

	params.PermissionReview = func(ctx context.Context, name string, args map[string]any, reason string) agent.PermReviewResult {
		// Steers are read live via the synchronized snapshot (agent goroutine)
		// so a mid-session toggle takes effect.
		if !m.autopilotEngaged() {
			return agent.PermReviewResult{}
		}
		cfg := m.liveAutopilotConfig()
		// Defense in depth: no steer may approve an unrecoverable action —
		// circuit-breaker tier included — even if one somehow reaches here.
		// The recoverable git tier is deliberately not checked — weighing it
		// is exactly what the judge is for.
		if r := setting.CircuitBreakerReason(name, args); r != "" {
			m.recordDecision(ctx, false, r)
			return agent.PermReviewResult{}
		}
		if r := setting.UnrecoverableReason(name, args); r != "" {
			m.recordDecision(ctx, false, r)
			return agent.PermReviewResult{}
		}
		// Permission steer: delegate gray-zone prompts to the judge. Off ⇒ every
		// gray-zone call escalates to the human.
		if !cfg.Steers.PermissionOn() {
			return agent.PermReviewResult{}
		}
		rev := m.autopilot.Load().judge
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		verdict, err := rev.Permission(ctx, reviewer.Request{
			ToolName: name, Args: args, Reason: reason, CWD: m.env.CWD, Mission: cfg.ActiveMission(),
			UnderGit: m.env.IsGit,
		})
		log.Logger().Debug("auto-review verdict",
			zap.String("tool", name),
			zap.Bool("allow", err == nil && verdict.Allow),
			zap.String("reason", verdict.Reason),
			zap.Error(err))
		if err != nil || !verdict.Allow {
			m.recordDecision(ctx, false, escalationReason(verdict.Reason, err))
			return agent.PermReviewResult{} // fail closed → escalate to human
		}
		if rec != nil {
			_ = rec.Append(context.Background(), transcript.Record{Type: transcript.PermissionDecided, Permission: &transcript.PermissionRecord{
				Tool: name, Input: marshalPermInput(args), Decision: permDecisionFor(true),
				Source: transcript.PermissionSourceReviewer, Reason: verdict.Reason, Mode: m.env.SessionMode(),
			}})
		}
		// Cache the approval as a session grant so an identical repeat hits the
		// static fast path instead of the judge, shrinking the gray zone over
		// the session. Only exact rules: a call no rule covers exactly asks the
		// judge again. Safe to write here: the agent goroutine is the only
		// mutator (the human-approval writer runs only while it is parked).
		for _, rule := range setting.ExactAllowRules(name, args) {
			m.env.SessionPermissions.AllowPattern(rule)
		}
		// Tally it and stash the decision so the renderer can draw it inline
		// under the tool call the judge just let through (the status-bar count
		// alone doesn't say what was approved or why).
		m.recordDecision(ctx, true, verdict.Reason)
		return agent.PermReviewResult{Allow: true, Reason: "autopilot: " + verdict.Reason}
	}

	params.HookAllowResolver = func(name string, args map[string]any) bool {
		return m.services.Setting.ResolveHookAllow(name, args, m.env.SessionPermissions)
	}

	params.StreamFirstChunkTimeout = streamFirstChunk
	params.StreamIdleTimeout = streamIdle

	return params, nil
}

// resolveReviewerModel picks the provider and model for the auto-review judge.
func (m *model) resolveReviewerModel(ref string) (llm.Provider, string) {
	p, info := m.resolveReviewer(ref)
	return p, info.ModelID
}

// resolveReviewer resolves a judge model ref to its provider and model. A
// "vendor/model" ref (e.g. "anthropic/claude-haiku-4-5") routes to that
// connected provider; a bare id stays on the session provider; empty uses the
// session model. An unresolvable vendor falls back to the session model. The
// info names whichever provider was actually picked, for capability lookups.
func (m *model) resolveReviewer(ref string) (llm.Provider, *llm.CurrentModelInfo) {
	session := &llm.CurrentModelInfo{ModelID: m.env.GetModelID()}
	if m.env.CurrentModel != nil {
		session.Provider, session.AuthMethod = m.env.CurrentModel.Provider, m.env.CurrentModel.AuthMethod
	}
	if ref == "" {
		return m.env.LLMProvider, session
	}
	if vendor, id, ok := llm.ParseVendorModel(ref); ok {
		if p, err := llm.NewProviderPool(m.services.LLM.Store()).Resolve(context.Background(), vendor); err == nil {
			return p, &llm.CurrentModelInfo{ModelID: id, Provider: vendor}
		}
		log.Logger().Warn("auto-review model vendor unavailable; using session model", zap.String("model", ref))
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
	p, info := m.resolveReviewer(ref)
	return llm.ThinkingEffortsForModel(p, m.services.LLM.Store(), info)
}

// autopilotModelRefs lists "vendor/model" refs for the /autopilot model picker:
// the cached models of each connected provider under its connected auth method.
func (m *model) autopilotModelRefs() []string {
	store := m.services.LLM.Store()
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

// recordDecision tallies one auto-review decision for the status-bar count and
// stashes it for the renderer, keyed by the tool call ID carried in ctx and
// consumed (load + delete) in TakeReviewDecision — so the handoff map only ever
// holds calls whose result has not arrived yet. The approved flag
// picks the counter, so the count and the inline annotations can never drift.
// The count is bumped even when ctx carries no tool call ID; only the stash
// needs one.
func (m *model) recordDecision(ctx context.Context, approved bool, reason string) {
	if approved {
		m.reviewerApprovals.Add(1)
	} else {
		m.reviewerEscalations.Add(1)
	}
	if id := core.ToolCallIDFromContext(ctx); id != "" {
		m.pendingDecisions.Store(id, core.ReviewDecision{Approved: approved, Reason: reason})
	}
}

// TakeReviewDecision consumes the decision stashed for a tool call (load + delete),
// so it stamps exactly one rendered result and the handoff map holds only
// in-flight calls. Returns nil when the call was not auto-reviewed. Mirrors the
// tool side-effect handoff (Tool.PopSideEffect), consumed at the same point in
// OnToolResult.
func (m *model) TakeReviewDecision(callID string) *core.ReviewDecision {
	// No empty-callID guard: recordDecision never stores under an empty key, so
	// LoadAndDelete("") already misses and returns nil.
	v, ok := m.pendingDecisions.LoadAndDelete(callID)
	if !ok {
		return nil
	}
	decision := v.(core.ReviewDecision)
	return &decision
}

// escalationReason is the one-line explanation shown when the judge sends a
// gray-zone call back to the user: the judge's own reason, or a generic note
// when it could not reach a decision (error / timeout / unparseable reply).
func escalationReason(judgeReason string, err error) string {
	if err != nil || judgeReason == "" {
		return "autopilot unavailable — asking you"
	}
	return judgeReason
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
	disabled := maps.Clone(m.services.Setting.DisabledTools())
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
			tool.ToolGroup:       "on while in a group",
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
	if m.services.Agent.Active() {
		// The toolset is fixed at agent-build time. If the enabled Evolve
		// capabilities or the disabled-tool set drifted since the build (a
		// /evolve save, a /tool toggle, an external settings edit), stop the
		// stale session and fall through to rebuild with the current toolset —
		// the one choke point every turn passes through, so all drift sources
		// are covered uniformly.
		if m.selfLearnCapabilities() == m.agentEvolveCaps &&
			m.disabledToolsSignature() == m.agentDisabledToolsSignature &&
			m.autoMemory() == m.agentAutoMemory {
			return nil, nil
		}
		m.StopAgentRuntime()
	}

	// Rebuild the subagent executor alongside the main agent so it inherits the
	// same effective disabled-tool snapshot after settings/persona drift.
	m.ReconfigureAgentTool()
	params, err := m.buildAgentParams()
	if err != nil {
		return nil, err
	}
	if w := m.services.Session; w != nil && w.Err() != nil {
		return nil, w.Err()
	}
	coreMessages := m.services.Agent.Messages()

	if err := m.services.Agent.Start(params, coreMessages); err != nil {
		return nil, err
	}
	// Record what the toolset was built with, for the drift check above.
	m.agentEvolveCaps = m.selfLearnCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	if m.systemRemindersSent && params.AutoMemory != m.agentAutoMemory {
		// The index reminder went out once, naming the old stores.
		m.services.Reminder.RequeueSystemReminders()
		if params.AutoMemory.Off() {
			m.services.Reminder.Enqueue("Auto memory is now off: stop reading and saving memory files.")
		}
	}
	m.agentAutoMemory = params.AutoMemory
	conv.MemoryStores = params.AutoMemory

	// Wire L1 self-learning *after* Agent.Start so the ReviewFunc can capture
	// the live Agent + System for its fork. Builds nothing if every skill action is
	// denied (§3.1 zero-overhead guarantee).
	m.wireSelfLearn(params)

	cmds := []tea.Cmd{
		conv.DrainAgentOutbox(m.services.Agent.Outbox()),
		conv.PollPermGate(m.services.Agent.PermissionGate()),
	}
	return tea.Batch(cmds...), nil
}

// agentSendFailedMsg reports a message the agent never accepted; a delivered
// message needs no round trip.
type agentSendFailedMsg struct{ err error }

// pendingMessage is input waiting for the agent. Only the user's own message
// picks memories: a harness message (notice, hook) would waste the pick, and
// mid-turn it would cancel the one the user's message started.
type pendingMessage struct {
	core.Message
	fromUser bool
}

func (m *model) sendToAgent(p pendingMessage) tea.Cmd {
	m.pendingMessages = append(m.pendingMessages, p)
	if p.fromUser {
		m.backgroundNoticesHeld = false
	}
	return m.sendPendingMessages()
}

func (m *model) sendPendingMessages() tea.Cmd {
	if m.backgroundNoticesHeld {
		return nil
	}
	if !m.services.Agent.Active() {
		return nil
	}
	if !m.conv.Stream.Active && len(m.pendingMessages) > 0 {
		m.turnOpenedBy = m.pendingMessages[0].ID
	}
	for len(m.pendingMessages) > 0 {
		next := &m.pendingMessages[0]
		if next.fromUser {
			m.startMemoryRecall(next.Text())
		}
		next.Message = m.attachPendingReminders(next.Message)
		// Keep rejected input in native form so the next submission can retry it.
		if err := m.services.Agent.Send(next.Message); err != nil {
			return func() tea.Msg { return agentSendFailedMsg{err: err} }
		}
		m.pendingMessages = m.pendingMessages[1:]
	}
	if !m.conv.Stream.Active {
		m.conv.Stream = conv.StreamState{Active: true, Waiting: true, StartedAt: time.Now()}
		return m.conv.Spinner.Tick
	}
	return nil
}

// memoryRecall is the pick running beside the current turn, read by the
// agent goroutine's result filter.
type memoryRecall struct {
	current  atomic.Pointer[agent.Recall]
	surfaced map[string]bool // attached in this conversation, never twice
}

// startMemoryRecall starts picking the memories a message sent to the agent
// needs, replacing a pick for an earlier one. The message does not wait.
func (m *model) startMemoryRecall(query string) {
	if m.memoryRecall == nil || strings.TrimSpace(query) == "" || conv.IsAgentEnvelope(query) || m.agentAutoMemory.Off() {
		return
	}
	if !m.systemRemindersSent || m.memoryRecall.surfaced == nil {
		m.memoryRecall.surfaced = map[string]bool{} // a new conversation has seen none
	}
	provider, info := m.resolveReviewer("")
	r := agent.Recaller{Provider: provider, Model: info.ModelID, Effort: agent.LowestEffort(provider, m.services.LLM.Store(), info)}
	m.memoryRecall.current.Swap(r.Start(m.agentAutoMemory, query, maps.Clone(m.memoryRecall.surfaced))).Stop()
}

// endMemoryRecall drops the turn's pick and returns what it attached, which
// later picks in this conversation skip.
func (m *model) endMemoryRecall() []string {
	if m.memoryRecall == nil {
		return nil
	}
	pick := m.memoryRecall.current.Swap(nil)
	pick.Stop()
	recalled := pick.Attached()
	for _, path := range recalled {
		m.memoryRecall.surfaced[path] = true
	}
	return recalled
}

// attachPendingReminders drains the reminder queue and appends any pending
// <system-reminder> blocks to the user message content. The harness uses this
// channel to deliver session/project context (skills, memory, one-time notices)
// without invalidating the system-prompt cache prefix.
func (m *model) attachPendingReminders(msg core.Message) core.Message {
	if !m.systemRemindersSent {
		m.services.Reminder.RequeueSystemReminders()
		m.systemRemindersSent = true
	}
	// Reminders (the roster among them) come before held member messages, so
	// the model reads who is who before what they said.
	text := m.attachWaitingMessages(reminder.AttachToContent(msg.Text(), m.services.Reminder.Drain()))
	if text == msg.Text() {
		return msg
	}
	content := ai.TextContent(text)
	for _, block := range msg.Content {
		if block.Type != ai.BlockText {
			content = append(content, block)
		}
	}
	msg.Content = content
	return msg
}

// wireReminderProviders registers the harness providers attached once per
// conversation (see systemRemindersSent). Each provider's render closure
// captures the services struct pointer so it always reads the live state
// — that way settings reload and skill toggles surface in the next emission
// without ever mutating the cached system prompt.
func (m *model) wireReminderProviders() {
	// Skill.PromptSection already produces a self-introduced body
	// ("Use the Skill tool to invoke these capabilities: ...") so it goes
	// inside <system-reminder> verbatim, matching Claude Code's shape.
	m.services.Reminder.Register(reminder.ProviderSkillsDirectory, func() string {
		return m.services.Skill.PromptSection()
	})
	m.services.Reminder.Register(reminder.ProviderMemoryUser, func() string {
		return reminder.WrapMemory("user", m.env.CachedUserInstructions)
	})
	m.services.Reminder.Register(reminder.ProviderMemoryProject, func() string {
		return reminder.WrapMemory("project", m.env.CachedProjectInstructions)
	})
	m.services.Reminder.Register(reminder.ProviderGroup, group.Roster)
	// Agent-written auto memory, read at render time so a PostCompact or cwd
	// change picks up the latest index. Kept as its own scope so agent-written
	// entries never mix with the user-authored memory above.
	m.services.Reminder.Register(reminder.ProviderMemoryAuto, func() string {
		return agent.AutoMemoryReminder(m.autoMemory())
	})
}

func (m *model) StopAgentRuntime() {
	m.services.Agent.Stop()
	m.teardownSelfLearn()
}

// ResetAgentRuntime stops the agent and clears its conversation and UI state.
func (m *model) ResetAgentRuntime() {
	m.services.Agent.Reset()
	m.backgroundNoticesHeld = false
	m.resetInteractions()
	// The next conversation carries neither the old notices nor, until its
	// first message, the skills/memory reminders.
	m.services.Reminder.DiscardPendingNotices()
	m.systemRemindersSent = false
	// Compaction must not restore the old conversation's files.
	m.env.FileCache.Clear()
	// Stop feeding the L1 reviewer AND cancel the session-scoped context
	// so an in-flight fork unblocks immediately instead of holding tokens /
	// HTTP for up to forkDeadline.
	m.teardownSelfLearn()
}

// ============================================================
// Agent outbox and permission gate
// ============================================================

func (m *model) AgentOutbox() <-chan core.Event { return m.services.Agent.Outbox() }

func (m *model) ContinueOutbox() tea.Cmd {
	outbox := m.services.Agent.Outbox()
	if outbox == nil {
		return nil
	}
	return conv.DrainAgentOutbox(outbox)
}

func (m *model) HandlePermGate(req *agent.PermGateRequest) tea.Cmd {
	if req == nil || (req.Context != nil && req.Context.Err() != nil) {
		return conv.PollPermGate(m.services.Agent.PermissionGate())
	}
	permissionView, err := m.preparePermissionRequest(req)
	if err != nil {
		// Preparation failures are tool errors, not requests for human consent.
		// The gate owns a buffered response channel; never enqueue an invalid
		// call or let a generic preview hide the actionable validation reason.
		select {
		case req.Response <- agent.PermGateResponse{Reason: fmt.Sprintf("cannot prepare %s approval: %v", req.ToolName, err)}:
		default:
		}
		return conv.PollPermGate(m.services.Agent.PermissionGate())
	}
	return tea.Batch(m.enqueueInteraction(&conv.InteractionRequest{
		Context: req.Context, Owner: m.conv.AgentToUI, Permission: req, PermissionView: permissionView,
		Source: tool.AgentInteractionSource{WorkingDirectory: m.env.CWD, PermissionMode: m.env.SessionMode()},
	}), conv.PollPermGate(m.services.Agent.PermissionGate()))
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

func (m *model) preparePermissionRequest(req *agent.PermGateRequest) (*perm.PermissionRequest, error) {
	permReq := &perm.PermissionRequest{
		ToolName:    req.ToolName,
		Description: req.Description,
	}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if resolved, ok := tool.Get(req.ToolName); ok {
		if pat, ok := resolved.(tool.PermissionAwareTool); ok {
			rich, err := pat.PreparePermission(ctx, req.Input, m.env.CWD)
			if err != nil {
				return nil, err
			}
			if rich != nil {
				permReq = rich
			}
		}
	}
	permReq.ID = req.RequestID
	permReq.WorkingDirectory = m.env.CWD
	permReq.PermissionMode = m.env.SessionMode()
	permReq.AllowRules = m.services.Setting.AlwaysAllowRules(req.ToolName, req.Input, m.env.SessionPermissions)
	// Session grants sit below allow rules, so neither would take effect here.
	if !m.services.Setting.SessionGrantTakesEffect(req.ToolName, req.Input, m.env.SessionPermissions) {
		permReq.OnceOnly = true
	}
	return permReq, nil
}

func (m *model) ReconfigureAgentTool() {
	if m.env.LLMProvider == nil {
		return
	}
	m.ensureMemoryContextLoaded()

	executor := subagent.NewExecutor(m.env.LLMProvider, m.env.CWD, m.env.GetModelID(), m.services.Hook)
	executor.SetInteractions(m.conv.AgentToUI)
	executor.SetDefaultModel(m.services.Setting.SubagentModel())
	executor.SetResolver(llm.NewProviderPool(m.services.LLM.Store()))
	perms := m.env.SessionPermissions
	executor.SetParentPermissionMode(func() subagent.PermissionMode {
		return subagent.InheritedPermissionMode(perms.CurrentMode())
	})
	if m.services.SessionStore != nil && m.sessionID() != "" {
		executor.SetSessionStore(m.services.SessionStore, m.sessionID())
	}
	executor.SetProjectInstructions(m.env.CachedProjectInstructions)
	executor.SetSkillsDirectory(m.services.Skill.PromptSection())
	executor.SetMCPDependencies(m.services.MCP)
	executor.SetDisabledTools(m.services.Setting.DisabledTools())

	adapter := subagent.NewExecutorAdapter(executor)
	type executorSetter interface{ SetExecutor(tool.AgentExecutor) }
	type searchPathSetter interface{ SetSearchPaths([]string) }
	home, _ := os.UserHomeDir()
	for _, name := range []string{tool.ToolAgent, tool.ToolSendMessage, tool.ToolWorkflow} {
		t, ok := m.services.Tool.Get(name)
		if !ok {
			continue
		}
		if setter, ok := t.(executorSetter); ok {
			setter.SetExecutor(adapter)
		}
		// Saved workflows resolve project-first, then user, the way subagent
		// definitions do. A home directory we could not read is left out
		// rather than joined from "", which would name the project twice.
		if setter, ok := t.(searchPathSetter); ok {
			dirs := []string{filepath.Join(confdir.Dir(m.env.CWD), "workflows")}
			if home != "" {
				dirs = append(dirs, filepath.Join(confdir.Dir(home), "workflows"))
			}
			setter.SetSearchPaths(dirs)
		}
	}
}

// ============================================================
// LLM client
// ============================================================

func (m *model) buildLLMClient() *llm.Client {
	c := llm.NewClient(m.env.LLMProvider, m.env.GetModelID(), kit.GetMaxOutput(m.services.LLM.Store(), m.env.CurrentModel, setting.DefaultMaxTokens))
	c.SetThinkingEffort(m.env.EffectiveThinkingEffort())
	return c
}
