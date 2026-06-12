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
	"github.com/genai-io/san/internal/worktree"
	"go.uber.org/zap"
)

// Executor runs agent LLM loops
type Executor struct {
	provider        llm.Provider
	cwd             string
	parentModelID   string // Parent conversation's model ID (used when inheriting)
	hooks           hook.Handler
	sessionStore    SubagentSessionStore // Optional: when set, subagent sessions are persisted
	parentSessionID string               // Parent session ID for linking subagent sessions
	isGit           bool                 // whether cwd is a git repository
	skillsPrompt    string               // available skills section for capable subagents
	agentsPrompt    string               // available agents section for capable subagents
	mcpTools        mcp.Tools            // tool schemas + execution
	mcpServers      mcp.Servers          // connect/disconnect for per-subagent server sets
}

type SubagentSessionStore interface {
	SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error)
	LoadSubagentMessages(agentID string) ([]core.Message, error)
}

type runConfig struct {
	config      *AgentConfig
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
// system prompt foundation as the parent conversation. Memory (user/project
// instructions) is intentionally not propagated — see collectSubagentReminders.
func (e *Executor) SetContext(isGit bool) {
	e.isGit = isGit
}

// SetCapabilities provides skills and agents prompt sections so subagents
// that have Agent/Skill tools can see available capabilities.
func (e *Executor) SetCapabilities(skillsPrompt, agentsPrompt string) {
	e.skillsPrompt = skillsPrompt
	e.agentsPrompt = agentsPrompt
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
func (e *Executor) Run(ctx context.Context, req tool.AgentExecRequest) (*AgentResult, error) {
	run, err := e.prepareRun(req)
	if err != nil {
		return nil, err
	}
	defer run.close()

	ctx = e.attachRunContext(ctx, run.cfg.displayName)
	e.logRunStart(run)
	e.fireSubagentStart(run.req, run.hookID)

	result, err := e.executePreparedRun(ctx, run)
	if err != nil && shouldRetryWithParentModel(err, run.cfg.modelID, e.parentModelID) {
		run.cfg.modelID = e.parentModelID
		result, err = e.executePreparedRun(ctx, run)
	}
	if err != nil {
		cancelled := e.buildCancelledAgentResult(run, result)
		if cancelled != nil {
			return cancelled, err
		}
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	return e.buildAgentResult(run, result), nil
}

// RunBackground executes an agent in the background and returns the task.
func (e *Executor) RunBackground(req tool.AgentExecRequest) (*task.AgentTask, error) {
	config, ok := defaultRegistry.Get(req.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", req.Agent)
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
	agentTask.SetIdentity(req.Agent, req.ResumeID)

	task.Default().RegisterTask(agentTask)

	req.OnProgress = func(msg string) {
		agentTask.AppendProgress(msg)
	}

	go func() {
		defer cancel()

		result, err := e.Run(ctx, req)
		if err != nil {
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

func (e *Executor) prepareWorkspace(req tool.AgentExecRequest) (string, func(), error) {
	if req.Isolation != "worktree" {
		return e.cwd, func() {}, nil
	}

	result, cleanup, err := worktree.Create(e.cwd, "")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create worktree: %w", err)
	}
	return result.Path, cleanup, nil
}

func (e *Executor) prepareRunConfig(req tool.AgentExecRequest) (*runConfig, error) {
	config, ok := defaultRegistry.Get(req.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %s", req.Agent)
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

	return &runConfig{
		config:      config,
		modelID:     e.resolveModelID(req.Model, config.Model),
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

func (e *Executor) buildAgent(ctx context.Context, rc *runConfig, agentCwd string, onToolExec func(string, map[string]any), onEvent func(core.Event)) (core.Agent, func(), error) {
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
	_, agentDirectoryGetter := e.capabilityPrompts(rc.config)

	sys := system.Build(core.ScopeSubagent,
		system.WithProvider(e.provider.Name()),
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
	toolSet.AgentDirectory = agentDirectoryGetter
	schemas := filterSchemasForPermission(toolSet.Tools(), rc.permMode, rc.config.AllowTools)
	var ag core.Agent
	tools := tool.AdaptToolRegistry(schemas, func() string { return agentCwd }, tool.WithMessagesGetterProvider(func() []core.Message {
		if ag == nil {
			return nil
		}
		return ag.Messages()
	}))

	// Add MCP tool executors
	if e.mcpTools != nil {
		mcpCaller := mcp.NewCaller(e.mcpTools)
		for _, t := range mcp.AsCoreTools(schemas, mcpCaller) {
			tools.Add(t, "mcp:"+t.Name())
		}
	}

	var coreTools core.Tools = tools
	if onToolExec != nil {
		coreTools = &progressTools{inner: tools, onExec: onToolExec}
	}

	// Wrap tools with permission decorator
	permFn := subagentPermissionFunc(rc.permMode, rc.config.AllowTools, rc.config.DenyTools)
	coreTools = tool.WithPermission(coreTools, permFn)

	ag = core.NewAgent(core.Config{
		LLM:       llm.NewClient(e.provider, rc.modelID, 0),
		System:    sys,
		Tools:     coreTools,
		AgentType: rc.config.Name,
		CWD:       agentCwd,
		MaxSteps:  rc.maxSteps,
		OutboxBuf: -1,
		OnEvent:   onEvent,
	})

	return ag, cleanup, nil
}

func (e *Executor) loadConversation(ag core.Agent, ctx context.Context, rc *runConfig, req tool.AgentExecRequest) error {
	// Resume from saved session
	if req.ResumeID != "" {
		if err := e.resumeFromSession(ag, ctx, req.ResumeID, req.Prompt); err != nil {
			return fmt.Errorf("failed to resume agent: %w", err)
		}
		return nil
	}

	// Fresh start: harness-managed reminders ride on the first user message
	// as <system-reminder> blocks, matching the main agent's pattern.
	skillsPrompt, _ := e.capabilityPrompts(rc.config)
	reminders := collectSubagentReminders(skillsPrompt)
	prompt := reminder.AttachToContent(req.Prompt, reminders)
	ag.Append(ctx, core.UserMessage(prompt, nil))
	return nil
}

// collectSubagentReminders returns the fully-wrapped <system-reminder> blocks
// for the subagent's first user message. Empty inputs produce no entries.
//
// Subagents get the skills directory (so they can invoke capabilities) but
// NOT user/project memory: memory is scoped to the long-lived main loop
// agent, whereas a subagent is a one-shot worker bounded by its own charter
// and should not carry the human's project/user instructions.
func collectSubagentReminders(skills string) []string {
	if w := reminder.Wrap(skills); w != "" {
		return []string{w}
	}
	return nil
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

func (e *Executor) fireSubagentStop(req tool.AgentExecRequest, agentHookID, agentSessionID, agentTranscriptPath, resultContent string) {
	if e.hooks == nil {
		return
	}

	stopAgentID := agentHookID
	if agentSessionID != "" {
		stopAgentID = agentSessionID
	}
	e.hooks.ExecuteAsync(hook.SubagentStop, hook.HookInput{
		AgentType:            req.Agent,
		AgentID:              stopAgentID,
		AgentTranscriptPath:  agentTranscriptPath,
		LastAssistantMessage: resultContent,
		StopHookActive:       e.hooks.StopHookActive(),
	})
}

// resolveModelID determines the model to use based on priority:
// 1. Explicit request override
// 2. Agent configuration
// 3. Parent conversation model
func (e *Executor) resolveModelID(requestModel string, configModel string) string {
	if requestModel != "" {
		return resolveModelAlias(requestModel)
	}
	if configModel != "" && configModel != "inherit" {
		return resolveModelAlias(configModel)
	}
	return e.parentModelID
}

func shouldRetryWithParentModel(err error, modelID, parentModelID string) bool {
	if err == nil || parentModelID == "" || modelID == "" || modelID == parentModelID {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "model_not_found") || strings.Contains(msg, "model not found") || strings.Contains(msg, "model_not_exist")
}

// modeChecker returns the perm.Checker for the mode. dontAsk falls back to
// default since the headless coercion is automatic for subagents; auto is
// aliased to acceptEdits until the safety classifier ships.
func modeChecker(mode PermissionMode) perm.Checker {
	switch NormalizePermissionMode(string(mode)) {
	case PermissionExplore:
		return perm.ReadOnly()
	case PermissionAcceptEdits, PermissionAuto:
		return perm.AcceptEdits()
	case PermissionBypass:
		return perm.PermitAll()
	default:
		return perm.Default()
	}
}

// capabilityPrompts returns the skills section body and a getter for the
// agent directory body that should be embedded into the Agent tool's
// description. Both are gated on the subagent's tool allow list — a subagent
// without the Skill tool gets no skills section, and one without the Agent
// tool gets no directory (the getter returns "").
func (e *Executor) capabilityPrompts(config *AgentConfig) (skillsPrompt string, agentDirectory func() string) {
	if config == nil {
		return "", nil
	}
	if config.AllowTools == nil || config.AllowTools.HasName("Skill") {
		skillsPrompt = e.skillsPrompt
	}
	if config.AllowTools == nil || config.AllowTools.HasName("Agent") {
		body := e.agentsPrompt
		agentDirectory = func() string { return body }
	}
	return skillsPrompt, agentDirectory
}

// subagentPermissionFunc returns the subagent permission gate. The pipeline
// matches docs/concepts/permission-model.md: deny_tools, bypass-immune, allow_tools,
// mode default, with Prompt collapsing to Deny because subagents cannot ask.
func subagentPermissionFunc(mode PermissionMode, allowRules, denyRules ToolList) perm.PermissionFunc {
	checker := modeChecker(mode)
	display := displayPermissionMode(mode)

	return func(_ context.Context, name string, input map[string]any) (bool, string) {
		if denyRules.Matches(name, input) {
			return false, fmt.Sprintf("tool %s is blocked by deny_tools", name)
		}
		if reason := setting.BypassImmuneReason(name, input); reason != "" {
			return false, fmt.Sprintf("tool %s blocked: %s", name, reason)
		}
		if allowRules.Allows(name, input) {
			return true, ""
		}
		// allow_tools mentions this tool but no pattern matched — the agent
		// declared a constrained whitelist, so deny rather than fall through.
		if allowRules.HasName(name) {
			return false, fmt.Sprintf("tool %s call is outside the allow_tools constraint", name)
		}
		switch checker.Check(name, input) {
		case perm.Permit:
			return true, ""
		case perm.Reject:
			return false, fmt.Sprintf("tool %s is denied in %s mode", name, display)
		default:
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
	if perm.IsSafeTool(name) {
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
