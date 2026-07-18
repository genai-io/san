package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"go.uber.org/zap"
)

// ProviderResolver turns a vendor name into a live provider so a subagent can
// run on a different vendor than its parent. The app wires an *llm.ProviderPool;
// a nil resolver disables cross-provider routing, in which case an explicit
// "vendor/model" override errors instead of silently using the parent provider.
type ProviderResolver interface {
	Resolve(ctx context.Context, provider llm.Name) (llm.Provider, error)
}

// Executor runs agent LLM loops
type Executor struct {
	provider            llm.Provider
	resolver            ProviderResolver // resolves "vendor/model" overrides; nil = same-provider only
	cwd                 string
	parentModelID       string // Parent conversation's model ID (used when inheriting)
	hooks               hook.Handler
	sessionStore        SubagentSessionStore // Optional: when set, subagent sessions are persisted
	parentSessionID     string               // Parent session ID for linking subagent sessions
	isGit               bool                 // whether cwd is a git repository
	projectInstructions string               // project memory (CLAUDE.md/AGENTS.md) for edit-capable subagents
	skillsPrompt        string               // available skills section for capable subagents
	mcpTools            mcp.Tools            // tool schemas + execution
	mcpServers          mcp.Servers          // connect/disconnect for per-subagent server sets
}

type SubagentSessionStore interface {
	SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error)
}

type runConfig struct {
	config      *AgentConfig
	provider    llm.Provider // provider this run talks to (parent's, or a routed vendor)
	modelID     string
	maxSteps    int
	displayName string
	brief       system.SubagentBrief // identity/charter for this run; immutable
	permMode    PermissionMode
}

// NewExecutor creates a new agent executor. parentModelID is used for model
// inheritance; hookEngine, when non-nil, fires subagent lifecycle hooks.
func NewExecutor(llmProvider llm.Provider, cwd string, parentModelID string, hookEngine hook.Handler) *Executor {
	return &Executor{
		provider:      llmProvider,
		cwd:           cwd,
		parentModelID: parentModelID,
		hooks:         hookEngine,
	}
}

// SetContext provides project context (git status) so subagents get the same
// system prompt foundation as the parent conversation. User memory is
// intentionally not propagated — see collectSubagentReminders.
func (e *Executor) SetContext(isGit bool) {
	e.isGit = isGit
}

// SetProjectInstructions provides the project's instruction memory
// (CLAUDE.md/AGENTS.md). Edit-capable subagents receive it as a
// <system-reminder> so their changes follow project conventions; read-only
// agents do not carry it.
func (e *Executor) SetProjectInstructions(instructions string) {
	e.projectInstructions = instructions
}

// SetResolver enables cross-provider routing: a subagent whose model is an
// explicit "vendor/model" override resolves through this resolver instead of
// reusing the parent's provider. Without it such overrides error rather than
// silently fall back to the parent.
func (e *Executor) SetResolver(r ProviderResolver) {
	e.resolver = r
}

// SetSkillsDirectory provides the skills directory section so subagents
// with the Skill tool can see and invoke available skills.
func (e *Executor) SetSkillsDirectory(skillsPrompt string) {
	e.skillsPrompt = skillsPrompt
}

// SetMCP wires the parent's MCP access for the subagent. Tool schemas
// and execution flow through tools; connection lifecycle for any
// per-subagent server set flows through servers.
func (e *Executor) SetMCP(tools mcp.Tools, servers mcp.Servers) {
	e.mcpTools = tools
	e.mcpServers = servers
}

// SetSessionStore configures session persistence for subagent conversations.
// When set, completed subagent conversations are saved under the parent session.
func (e *Executor) SetSessionStore(store SubagentSessionStore, parentSessionID string) {
	e.sessionStore = store
	e.parentSessionID = parentSessionID
}

// GetParentModelID returns the parent model ID
func (e *Executor) GetParentModelID() string {
	return e.parentModelID
}

// Run executes an agent request and returns the result.
// For background agents, this should be called in a goroutine.
//
// Every exit path fires the SubagentStop hook with the same AgentID the
// SubagentStart hook carried. Cancelled runs persist their conversation and
// partial output for inspection.
func (e *Executor) Run(ctx context.Context, req tool.AgentExecRequest) (*AgentResult, error) {
	run, err := e.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}

	ctx = e.attachRunContext(ctx, run.cfg.displayName)
	e.logRunStart(run)
	e.fireSubagentStart(run.req, run.hookID)

	result, err := e.executePreparedRun(ctx, run)
	if err != nil && shouldRetryWithParentModel(err, run.cfg.modelID, e.parentModelID) {
		run.cfg.provider = e.provider
		run.cfg.modelID = e.parentModelID
		result, err = e.executePreparedRun(ctx, run)
	}
	if err != nil {
		cancelled := e.buildCancelledAgentResult(run, result)
		if cancelled != nil {
			return cancelled, err
		}
		e.fireSubagentStop(run.req, run.hookID, "", "")
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	return e.buildAgentResult(run, result), nil
}

// RunBackground executes an agent in the background and returns the task.
func (e *Executor) RunBackground(req tool.AgentExecRequest) (*task.AgentTask, error) {
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}
	config, ok := defaultRegistry.Get(req.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", req.Agent)
	}
	if !defaultRegistry.IsEnabled(req.Agent) {
		return nil, fmt.Errorf("agent type is disabled: %s", req.Agent)
	}

	ctx, cancel := context.WithCancel(context.Background())
	displayName := displayNameFor(config, req)

	agentTask := task.NewAgentTask(
		generateShortID(),
		displayName,
		req.Description,
		ctx,
		cancel,
	)
	agentTask.SetIdentity(req.Agent, "")

	task.Default().RegisterTask(agentTask)

	req.TaskID = agentTask.GetID()
	req.OnActivity = func(msg string) {
		agentTask.AppendProgress(msg)
	}
	// Background subagents run unattended: no interactive question channel.
	req.OnQuestion = nil

	go func() {
		defer cancel()

		result, err := e.Run(ctx, req)
		if err != nil {
			// A cancelled run still returns its partial work — keep it in the
			// task output and record its persisted session for inspection.
			if result != nil {
				if result.Content != "" {
					agentTask.AppendOutput([]byte(result.Content + "\n"))
				}
				agentTask.SetIdentity(req.Agent, result.AgentID)
				agentTask.UpdateProgress(result.StepCount, result.TokenUsage.InputTokens+result.TokenUsage.OutputTokens)
			}
			agentTask.AppendOutput([]byte(fmt.Sprintf("Error: %v\n", err)))
			agentTask.Complete(err)
			return
		}

		if result.Content != "" {
			agentTask.AppendOutput([]byte(result.Content))
		}

		agentTask.SetIdentity(req.Agent, result.AgentID)
		agentTask.SetOutputFile(result.TranscriptPath)
		agentTask.UpdateProgress(result.StepCount, result.TokenUsage.InputTokens+result.TokenUsage.OutputTokens)

		if result.Success {
			agentTask.Complete(nil)
		} else {
			agentTask.Complete(fmt.Errorf("%s", result.Error))
		}
	}()

	return agentTask, nil
}

func (e *Executor) validateRequest(req tool.AgentExecRequest) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("agent prompt cannot be empty")
	}
	return nil
}

func (e *Executor) prepareRunConfig(ctx context.Context, req tool.AgentExecRequest) (*runConfig, error) {
	config, ok := defaultRegistry.Get(req.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", req.Agent)
	}
	if !defaultRegistry.IsEnabled(req.Agent) {
		return nil, fmt.Errorf("agent type is disabled: %s", req.Agent)
	}

	displayName := displayNameFor(config, req)

	permMode := requestPermissionMode(config, req)

	maxSteps := config.MaxSteps
	if req.MaxSteps > maxSteps {
		maxSteps = req.MaxSteps
	}
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	provider, modelID, err := e.resolveModel(ctx, req.Model, config.Model)
	if err != nil {
		return nil, err
	}

	return &runConfig{
		config:      config,
		provider:    provider,
		modelID:     modelID,
		maxSteps:    maxSteps,
		displayName: displayName,
		brief:       e.buildBrief(config, permMode),
		permMode:    permMode,
	}, nil
}

func (e *Executor) fireSubagentStart(req tool.AgentExecRequest, agentHookID string) {
	if e.hooks == nil {
		return
	}
	e.hooks.ExecuteAsync(hook.SubagentStart, hook.HookInput{
		AgentType:   req.Agent,
		AgentID:     agentHookID,
		Description: req.Description,
	})
}

func (e *Executor) buildAgent(ctx context.Context, run *preparedRun, onToolExec func(string, map[string]any), onEvent func(core.Event)) (core.Agent, func(), error) {
	rc := run.cfg
	agentCwd := run.cwd
	cleanup := func() {}

	if len(rc.config.McpServers) > 0 && e.mcpServers != nil {
		mcpCleanup, errs := mcp.ConnectServers(ctx, e.mcpServers, rc.config.McpServers)
		if mcpCleanup != nil {
			cleanup = mcpCleanup
		}
		for _, err := range errs {
			log.Logger().Warn("Agent MCP server connection failed", zap.Error(err))
		}
	}

	// Subagent system prompt deliberately omits skills and memory — those
	// ride on the first user message as <system-reminder> blocks built by
	// loadConversation, keeping subagents on the same harness channel
	// pattern as the main agent.
	sys := system.Build(core.ScopeSubagent,
		system.WithProvider(rc.provider.Name()),
		system.WithGitGuidelines(e.isGit),
		system.WithSubagentIdentity(rc.brief),
		system.WithEnvironment(system.Environment{
			Cwd:     agentCwd,
			IsGit:   e.isGit,
			ModelID: rc.modelID,
		}),
	)

	// Tools — adapt legacy tool registry + MCP tools
	var mcpGetter func() []core.ToolSchema
	if e.mcpTools != nil {
		mcpGetter = e.mcpTools.GetToolSchemas
	}
	toolSet := newAgentToolSet(rc.config.AllowTools.Names(), rc.config.DenyTools.BareNames(), mcpGetter)
	schemas := filterSchemasForPermission(toolSet.Tools(), rc.permMode, rc.config.AllowTools)
	var ag core.Agent
	adaptOpts := []tool.AdaptOption{tool.WithMessagesGetterProvider(func() []core.Message {
		if ag == nil {
			return nil
		}
		return ag.Messages()
	})}
	// Foreground runs route AskUserQuestion through the spawning turn's UI.
	// Background runs have no question channel (OnQuestion is stripped).
	if run.req.OnQuestion != nil {
		adaptOpts = append(adaptOpts, tool.WithAskUser(tool.AskUserFunc(run.req.OnQuestion)))
	}
	tools := tool.AdaptToolRegistry(schemas, func() string { return agentCwd }, adaptOpts...)

	// Add MCP tool executors
	if e.mcpTools != nil {
		mcpCaller := mcp.NewCaller(e.mcpTools)
		for _, t := range mcp.AsCoreTools(schemas, mcpCaller) {
			tools.Add(t, "mcp:"+t.Name())
		}
	}

	var coreTools core.Tools = tools
	if onToolExec != nil {
		coreTools = &activityTools{inner: tools, onExec: onToolExec}
	}

	// Wrap tools with permission decorator
	permFn := subagentPermissionFunc(rc.permMode, rc.config.AllowTools, rc.config.DenyTools)
	coreTools = tool.WithPermission(coreTools, permFn)

	llmClient := llm.NewClient(rc.provider, rc.modelID, 0)
	ag = core.NewAgent(core.Config{
		LLM:         llmClient,
		System:      sys,
		Tools:       coreTools,
		AgentType:   rc.config.Name,
		CompactFunc: subagentCompactFunc(llmClient),
		CWD:         agentCwd,
		MaxSteps:    rc.maxSteps,
		OutboxBuf:   -1,
		OnEvent:     onEvent,
	})

	return ag, cleanup, nil
}

// subagentCompactFunc summarizes the conversation on the run's own model so
// long subagent runs survive context-window pressure instead of dying on
// prompt-too-long. Mirrors the main agent's compaction.
func subagentCompactFunc(client *llm.Client) func(context.Context, []core.Message) (string, error) {
	return func(ctx context.Context, msgs []core.Message) (string, error) {
		text := core.BuildCompactionText(msgs)
		resp, err := client.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(text, nil)}, core.CompactMaxTokens)
		if err != nil {
			return "", err
		}
		summary := strings.TrimSpace(resp.Content)
		if summary == "" {
			return "", fmt.Errorf("compaction produced empty summary")
		}
		return summary, nil
	}
}

func (e *Executor) loadConversation(ag core.Agent, ctx context.Context, rc *runConfig, req tool.AgentExecRequest) error {
	// Harness-managed reminders ride on the first user message as
	// <system-reminder> blocks, matching the main agent's pattern.
	reminders := e.collectSubagentReminders(e.skillsDirectoryFor(rc.config), rc.permMode, rc.config.AllowTools)
	prompt := reminder.AttachToContent(req.Prompt, reminders)
	ag.Append(ctx, core.UserMessage(prompt, nil))
	return nil
}

// collectSubagentReminders returns the <system-reminder> blocks for the
// subagent's first user message. Subagents get the skills directory (so they
// can invoke capabilities) and — when they can modify the workspace — the
// project's instruction memory, so their edits follow project conventions.
// User memory stays with the main loop: a subagent is a one-shot worker
// bounded by its own charter.
func (e *Executor) collectSubagentReminders(skills string, mode PermissionMode, allow ToolList) []string {
	var reminders []string
	reminders = append(reminders, wrapNonEmpty(skills)...)
	if canEditWorkspace(mode, allow) {
		reminders = append(reminders, wrapNonEmpty(reminder.WrapMemory("project", e.projectInstructions))...)
	}
	return reminders
}

func wrapNonEmpty(body string) []string {
	if w := reminder.Wrap(body); w != "" {
		return []string{w}
	}
	return nil
}

// canEditWorkspace reports whether the subagent can change files in the
// workspace — the signal for whether project conventions are worth handing it.
// True when the permission mode auto-accepts mutations (edit/bypass), or when
// an explicit allow_tools rule grants an edit-class tool (Edit/Write/…) even
// under a mode, such as default, that would otherwise deny them.
func canEditWorkspace(mode PermissionMode, allow ToolList) bool {
	switch operationMode(mode) {
	case setting.ModeAutoAccept, setting.ModeBypassPermissions:
		return true
	}
	for _, name := range allow.Names() {
		if perm.IsEditTool(name) {
			return true
		}
	}
	return false
}

func interpretStopReason(result *core.Result, maxSteps int) (success bool, errMsg string) {
	success = result.StopReason == core.StopEndTurn
	switch result.StopReason {
	case core.StopMaxSteps:
		errMsg = fmt.Sprintf("reached maximum steps (%d)", maxSteps)
	case core.StopMaxOutputRecoveryExhausted:
		errMsg = "output was repeatedly truncated and recovery was exhausted"
	case core.StopHook:
		errMsg = result.StopDetail
	}
	return success, errMsg
}

// fireSubagentStop fires on every run exit — success, cancellation, or error
// — with the same AgentID that SubagentStart carried, so hook consumers can
// pair the two events. The session id travels via the transcript path.
func (e *Executor) fireSubagentStop(req tool.AgentExecRequest, agentHookID, agentTranscriptPath, resultContent string) {
	if e.hooks == nil {
		return
	}

	e.hooks.ExecuteAsync(hook.SubagentStop, hook.HookInput{
		AgentType:            req.Agent,
		AgentID:              agentHookID,
		AgentTranscriptPath:  agentTranscriptPath,
		LastAssistantMessage: resultContent,
		StopHookActive:       e.hooks.StopHookActive(),
	})
}

// resolveModel picks the provider and model id for a run, by priority:
// 1. Explicit request override (req.Model)
// 2. Agent configuration (config.Model)
// 3. Parent conversation model ("inherit" or empty)
//
// An explicit "vendor/model" override routes to that vendor through the
// resolver, erroring if the vendor is not connected. Every other form — an
// alias, a bare model id, or "inherit" — stays on the parent's provider,
// preserving prior behavior.
func (e *Executor) resolveModel(ctx context.Context, requestModel, configModel string) (llm.Provider, string, error) {
	ref := requestModel
	if ref == "" {
		ref = configModel
	}
	if ref == "" || ref == "inherit" {
		return e.provider, e.parentModelID, nil
	}
	if vendor, modelID, ok := llm.ParseVendorModel(ref); ok {
		if e.resolver == nil {
			return nil, "", fmt.Errorf("model %q routes to provider %q, but cross-provider routing is unavailable", ref, vendor)
		}
		p, err := e.resolver.Resolve(ctx, vendor)
		if err != nil {
			return nil, "", fmt.Errorf("model %q: %w", ref, err)
		}
		return p, modelID, nil
	}
	return e.provider, resolveModelAlias(ref), nil
}

func shouldRetryWithParentModel(err error, modelID, parentModelID string) bool {
	if err == nil || parentModelID == "" || modelID == "" || modelID == parentModelID {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "model_not_found") || strings.Contains(msg, "model not found") || strings.Contains(msg, "model_not_exist")
}

// operationMode maps a subagent PermissionMode to the setting.OperationMode that
// drives the shared mode-default table (setting.ModeDefault). dontAsk folds to a
// read-only-style denial since subagents never prompt; auto aliases to
// acceptEdits until the safety classifier ships.
func operationMode(mode PermissionMode) setting.OperationMode {
	switch NormalizePermissionMode(string(mode)) {
	case PermissionExplore:
		return setting.ModeReadOnly
	case PermissionAcceptEdits, PermissionAuto:
		return setting.ModeAutoAccept
	case PermissionBypass:
		return setting.ModeBypassPermissions
	case PermissionDontAsk:
		return setting.ModeDontAsk
	default:
		return setting.ModeNormal
	}
}

// skillsDirectoryFor returns the skills section body for the agent, gated on
// its tool allow list — a subagent without the Skill tool gets no skills
// section.
func (e *Executor) skillsDirectoryFor(config *AgentConfig) string {
	if config == nil {
		return ""
	}
	if config.AllowTools == nil || config.AllowTools.HasName("Skill") {
		return e.skillsPrompt
	}
	return ""
}

// subagentPermissionFunc returns the subagent permission gate. The pipeline
// matches docs/concepts/permission-model.md: deny_tools, bypass-immune, allow_tools,
// mode default, with Prompt collapsing to Deny because subagents cannot ask.
//
// One communication carve-out sits between deny and allow_tools: for a
// mode-gated worker (no explicit allow_tools list) SendMessage is permitted in
// every mode, so even a read-only or default worker can report to main or
// answer a peer — it only moves text between agents and never touches the
// workspace. deny_tools still blocks it, and an explicit allow_tools list is
// honored as written (leave SendMessage out to silence a worker). Spawning is
// not gated here: the Agent tool is parent-only, so workers never see it — the
// agent model is flat.
func subagentPermissionFunc(mode PermissionMode, allowRules, denyRules ToolList) perm.PermissionFunc {
	opMode := operationMode(mode)
	display := displayPermissionMode(mode)

	return func(_ context.Context, name string, input map[string]any) (bool, string) {
		if denyRules.Matches(name, input) {
			return false, fmt.Sprintf("tool %s is blocked by deny_tools", name)
		}
		if reason := setting.BypassImmuneReason(name, input); reason != "" {
			return false, fmt.Sprintf("tool %s blocked: %s", name, reason)
		}
		// Communication carve-out (see doc comment): a mode-gated worker may
		// always reach main or a peer via SendMessage. A worker with an explicit
		// allow_tools list is governed by that list instead.
		if name == tool.ToolSendMessage && allowRules == nil {
			return true, ""
		}
		if allowRules.Allows(name, input) {
			return true, ""
		}
		// allow_tools mentions this tool but no pattern matched — the agent
		// declared a constrained whitelist, so deny rather than fall through.
		if allowRules.HasName(name) {
			return false, fmt.Sprintf("tool %s call is outside the allow_tools constraint", name)
		}
		switch setting.ModeDefault(name, opMode).Behavior {
		case perm.Permit:
			return true, ""
		case perm.Reject:
			return false, fmt.Sprintf("tool %s is denied in %s mode", name, display)
		default: // perm.Prompt — subagents cannot ask
			return false, fmt.Sprintf("tool %s would require approval; subagent in %s mode denies it", name, display)
		}
	}
}

// filterSchemasForPermission narrows the LLM-visible tool set to what the
// agent can actually use under its mode + allow_tools. UX hint only — the
// permission gate is still authoritative. A non-nil allowTools acts as a
// whitelist regardless of mode.
func filterSchemasForPermission(schemas []core.ToolSchema, mode PermissionMode, allowTools ToolList) []core.ToolSchema {
	mode = NormalizePermissionMode(string(mode))
	whitelist := allowTools != nil

	filtered := make([]core.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		if whitelist {
			if allowTools.HasName(schema.Name) {
				filtered = append(filtered, schema)
			}
			continue
		}
		if modeAllowsSchema(mode, schema.Name) {
			filtered = append(filtered, schema)
		}
	}
	return filtered
}

func modeAllowsSchema(mode PermissionMode, name string) bool {
	// SendMessage is a communication tool, kept visible in every mode so a
	// mode-gated worker can report to main (the gate permits it — see
	// subagentPermissionFunc). A worker with an explicit allow_tools list takes
	// the whitelist branch in filterSchemasForPermission instead of this one.
	if perm.IsSafeTool(name) || name == tool.ToolSendMessage {
		return true
	}
	switch mode {
	case PermissionBypass, PermissionAuto:
		return true
	case PermissionAcceptEdits:
		return perm.IsEditTool(name)
	}
	return false
}

// newAgentToolSet creates a tool.Set for subagents with the disallow set eagerly initialized.
func newAgentToolSet(allow, disallow []string, mcpGetter func() []core.ToolSchema) *tool.Set {
	s := &tool.Set{Allow: allow, Disallow: disallow, MCP: mcpGetter, IsAgent: true}
	s.InitDisallowSet()
	return s
}

// generateShortID creates a short random hex ID for background tasks.
func generateShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
