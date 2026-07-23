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
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// ProviderResolver turns a vendor name into a live provider so a subagent can
// run on a different vendor than its parent. The app wires an *llm.ProviderPool;
// unresolved explicit "vendor/model" overrides fall back to the parent provider.
type ProviderResolver interface {
	Resolve(ctx context.Context, provider llm.Name) (llm.Provider, error)
}

// Executor runs agent LLM loops
type Executor struct {
	provider             llm.Provider
	resolver             ProviderResolver // resolves "vendor/model" overrides; nil = same-provider only
	modelStore           *llm.Store       // optional cached provider catalog for validating same-provider overrides
	parentProviderName   llm.Name         // canonical provider key for the parent connection
	parentAuthMethod     llm.AuthMethod   // auth-specific catalog key for the parent connection
	cwd                  string
	parentModelID        string // Parent conversation's model ID (used when inheriting)
	parentPermissionMode func() PermissionMode
	hooks                hook.Handler
	sessionStore         SubagentSessionStore // Optional: when set, subagent sessions are persisted
	parentSessionID      string               // Parent session ID for linking subagent sessions
	projectInstructions  string               // project memory (CLAUDE.md/AGENTS.md) for edit-capable subagents
	skillsPrompt         string               // available skills section for capable subagents
	mcpTools             mcp.Tools            // tool schemas + execution
}

type SubagentSessionStore interface {
	SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error)
}

type runConfig struct {
	provider    llm.Provider // provider this run talks to (parent's, or a routed vendor)
	modelID     string
	maxSteps    int
	displayName string
	brief       system.SubagentBrief // identity/charter for this run; immutable
	permMode    PermissionMode
}

// PermissionModeFromOperationMode preserves the parent session's effective
// policy for mode="default" without exposing privileged spellings in the tool
// schema.
func PermissionModeFromOperationMode(mode setting.OperationMode) PermissionMode {
	switch mode {
	case setting.ModeAutoAccept, setting.ModeAutoPilot:
		return PermissionAcceptEdits
	case setting.ModeBypassPermissions:
		return PermissionBypass
	case setting.ModeDontAsk:
		return PermissionDontAsk
	case setting.ModeReadOnly:
		return PermissionExplore
	default:
		return PermissionDefault
	}
}

// NewExecutor creates a new agent executor. parentModelID is used for model
// inheritance; hookEngine, when non-nil, fires subagent lifecycle hooks.
// Headless callers inherit the safe default permission policy unless they set a
// parent permission mode getter.
func NewExecutor(llmProvider llm.Provider, cwd string, parentModelID string, hookEngine hook.Handler) *Executor {
	return &Executor{
		provider:      llmProvider,
		cwd:           cwd,
		parentModelID: parentModelID,
		hooks:         hookEngine,
	}
}

// SetParentPermissionMode provides the parent session's live permission mode.
// It is evaluated for every run so mode="default" follows mode changes made
// after the executor was configured, including entering bypass mode.
func (e *Executor) SetParentPermissionMode(getMode func() PermissionMode) {
	e.parentPermissionMode = getMode
}

func (e *Executor) currentParentPermissionMode() PermissionMode {
	if e.parentPermissionMode == nil {
		return PermissionDefault
	}
	return NormalizePermissionMode(string(e.parentPermissionMode()))
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
// reusing the parent's provider. Unavailable routes fall back to the parent.
func (e *Executor) SetResolver(r ProviderResolver) {
	e.resolver = r
}

// SetModelStore supplies the cached catalog and parent connection identity used
// to reject unsupported same-provider overrides without fetching models on the
// agent startup path.
func (e *Executor) SetModelStore(store *llm.Store, provider llm.Name, authMethod llm.AuthMethod) {
	e.modelStore = store
	e.parentProviderName = provider
	e.parentAuthMethod = authMethod
}

// SetSkillsDirectory provides the skills directory section so subagents
// with the Skill tool can see and invoke available skills.
func (e *Executor) SetSkillsDirectory(skillsPrompt string) {
	e.skillsPrompt = skillsPrompt
}

// SetMCP wires the parent's MCP tools for the subagent.
func (e *Executor) SetMCP(tools mcp.Tools) {
	e.mcpTools = tools
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
		if unfinished := e.buildUnfinishedAgentResult(run, result); unfinished != nil {
			return unfinished, err
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
	ctx, cancel := context.WithCancel(context.Background())
	displayName := e.displayNameFor(req)

	agentTask := task.NewAgentTask(
		generateShortID(),
		displayName,
		req.Description,
		ctx,
		cancel,
	)
	agentTask.SetIdentity("subagent", "")

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
				agentTask.SetIdentity("subagent", result.AgentID)
				agentTask.UpdateProgress(result.StepCount, result.TokenUsage.InputTokens+result.TokenUsage.OutputTokens)
			}
			agentTask.AppendOutput([]byte(fmt.Sprintf("Error: %v\n", err)))
			agentTask.Complete(err)
			return
		}

		if result.Content != "" {
			agentTask.AppendOutput([]byte(result.Content))
		}

		agentTask.SetIdentity("subagent", result.AgentID)
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
	switch strings.TrimSpace(req.Mode) {
	case "", "default", "explore", "edit":
		return nil
	default:
		return fmt.Errorf("invalid agent mode %q: must be explore, edit, or default", req.Mode)
	}
}

func (e *Executor) prepareRunConfig(ctx context.Context, req tool.AgentExecRequest) (*runConfig, error) {
	displayName := e.displayNameFor(req)
	permMode := e.requestPermissionMode(req)

	maxSteps := defaultMaxSteps
	if req.MaxSteps > maxSteps {
		maxSteps = req.MaxSteps
	}

	provider, modelID, err := e.resolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	return &runConfig{
		provider:    provider,
		modelID:     modelID,
		maxSteps:    maxSteps,
		displayName: displayName,
		brief:       e.buildBrief(permMode),
		permMode:    permMode,
	}, nil
}

func (e *Executor) fireSubagentStart(req tool.AgentExecRequest, agentHookID string) {
	if e.hooks == nil {
		return
	}
	e.hooks.ExecuteAsync(hook.SubagentStart, hook.HookInput{
		AgentType:   "subagent",
		AgentID:     agentHookID,
		Description: req.Description,
	})
}

func (e *Executor) buildAgent(ctx context.Context, run *preparedRun, onToolExec func(string, map[string]any), onEvent func(core.Event)) (core.Agent, func(), error) {
	rc := run.cfg
	agentCwd := run.cwd
	cleanup := func() {}

	// Subagent system prompt deliberately omits skills and memory — those
	// ride on the first user message as <system-reminder> blocks built by
	// loadConversation, keeping subagents on the same harness channel pattern.
	sys := system.Build(core.ScopeSubagent,
		system.WithSubagentIdentity(rc.brief),
		system.WithEnvironment(system.Environment{Cwd: agentCwd}),
	)

	// Tools — adapt legacy tool registry + MCP tools
	var mcpGetter func() []core.ToolSchema
	if e.mcpTools != nil {
		mcpGetter = e.mcpTools.GetToolSchemas
	}
	toolSet := newAgentToolSet(mcpGetter)
	schemas := filterSchemasForPermission(toolSet.Tools(), rc.permMode)
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
	permFn := subagentPermissionFunc(rc.permMode)
	coreTools = tool.WithPermission(coreTools, permFn)

	llmClient := llm.NewClient(rc.provider, rc.modelID, 0)
	ag = core.NewAgent(core.Config{
		LLM:         llmClient,
		System:      sys,
		Tools:       coreTools,
		AgentType:   "subagent",
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
	reminders := e.collectSubagentReminders(rc.permMode)
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
func (e *Executor) collectSubagentReminders(mode PermissionMode) []string {
	reminders := wrapNonEmpty(e.skillsPrompt)
	if canEditWorkspace(mode) {
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

// canEditWorkspace reports whether the effective mode can change files — the
// signal for whether project conventions should be handed to the subagent.
func canEditWorkspace(mode PermissionMode) bool {
	switch operationMode(mode) {
	case setting.ModeAutoAccept, setting.ModeBypassPermissions:
		return true
	default:
		return false
	}
}

func interpretStopReason(result *core.Result, maxSteps int) (success bool, errMsg string) {
	success = result.StopReason == core.StopEndTurn
	switch result.StopReason {
	case core.StopMaxSteps:
		errMsg = fmt.Sprintf("reached maximum steps (%d)", maxSteps)
	case core.StopMaxOutputRecoveryExhausted:
		errMsg = "output was repeatedly truncated and recovery was exhausted"
	case core.StopCancelled:
		errMsg = "agent cancelled"
	case core.StopHook, core.StopError:
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
		AgentType:            "subagent",
		AgentID:              agentHookID,
		AgentTranscriptPath:  agentTranscriptPath,
		LastAssistantMessage: resultContent,
		StopHookActive:       e.hooks.StopHookActive(),
	})
}

// resolveModel picks the provider and model id for a run. An empty request or
// "inherit" uses the parent conversation model.
//
// An explicit "vendor/model" override routes to that vendor through the
// resolver only when a linked provider can be resolved. Otherwise it falls
// back to the parent conversation. Every other form — an alias or a bare model
// id — stays on the parent's provider, preserving prior behavior.
func (e *Executor) resolveModel(ctx context.Context, requestModel string) (llm.Provider, string, error) {
	ref := strings.TrimSpace(requestModel)
	if ref == "" || ref == "inherit" {
		return e.provider, e.parentModelID, nil
	}
	if vendor, modelID, ok := llm.ParseVendorModel(ref); ok {
		if e.resolver == nil {
			return e.provider, e.parentModelID, nil
		}
		if vendor == e.parentProviderName && modelID != e.parentModelID && !e.modelAvailable(modelID) {
			return e.provider, e.parentModelID, nil
		}
		p, err := e.resolver.Resolve(ctx, vendor)
		if err != nil || p == nil {
			return e.provider, e.parentModelID, nil
		}
		return p, modelID, nil
	}
	// A bare id or alias stays on the parent provider. If the cached catalog
	// positively reports that provider does not offer the model, inherit instead
	// of sending a request that may fail with an opaque 400 response.
	modelID := resolveModelAlias(ref)
	if modelID != e.parentModelID && !e.modelAvailable(modelID) {
		return e.provider, e.parentModelID, nil
	}
	return e.provider, modelID, nil
}

// modelAvailable checks the parent provider's cached model catalog. A missing
// store, parent connection identity, or catalog leaves the override unverified
// and therefore allowed; only a definitive cached miss rejects it.
func (e *Executor) modelAvailable(modelID string) bool {
	if e.modelStore == nil || e.parentProviderName == "" || modelID == "" {
		return true
	}
	models, ok := e.modelStore.GetCachedModels(e.parentProviderName, e.parentAuthMethod)
	if !ok {
		return true
	}
	for _, model := range models {
		if model.ID == modelID {
			return true
		}
	}
	return false
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

// subagentPermissionFunc returns the fixed subagent permission gate. It uses the
// shared mode defaults but converts any interactive Prompt decision to Deny.
// Bypass skips confirmation tiers, while the root/home-removal circuit breaker
// and parent-only tool boundary always hold.
func subagentPermissionFunc(mode PermissionMode) perm.PermissionFunc {
	mode = NormalizePermissionMode(string(mode))
	opMode := operationMode(mode)
	display := displayPermissionMode(mode)

	return func(_ context.Context, name string, input map[string]any) (bool, string) {
		if reason := setting.CircuitBreakerReason(name, input); reason != "" {
			return false, fmt.Sprintf("tool %s blocked: %s", name, reason)
		}
		if tool.IsParentOnlyTool(name) {
			return false, fmt.Sprintf("tool %s is reserved for the main conversation", name)
		}
		if opMode == setting.ModeBypassPermissions {
			return true, ""
		}
		if reason, _ := setting.ConfirmationTier(name, input); reason != "" {
			return false, fmt.Sprintf("tool %s blocked: %s", name, reason)
		}
		if mode == PermissionExplore {
			switch {
			case perm.IsSafeTool(name), name == tool.ToolSendMessage, name == tool.ToolSkill:
				return true, ""
			case name == tool.ToolBash:
				command, _ := input["command"].(string)
				if setting.IsReadOnlyBashCommand(command) {
					return true, ""
				}
			}
			return false, fmt.Sprintf("tool %s is denied in %s mode", name, display)
		}
		if name == tool.ToolSendMessage || name == tool.ToolSkill {
			return true, ""
		}
		if name == tool.ToolBash {
			if command, ok := input["command"].(string); ok && setting.IsReadOnlyBashCommand(command) {
				return true, ""
			}
		}
		switch setting.ModeDefault(name, opMode).Behavior {
		case perm.Permit:
			return true, ""
		case perm.Reject:
			return false, fmt.Sprintf("tool %s is denied in %s mode", name, display)
		default:
			return false, fmt.Sprintf("tool %s would require approval; subagent in %s mode denies it", name, display)
		}
	}
}

// filterSchemasForPermission narrows the model-visible tools to those usable
// under the effective mode. The permission gate remains authoritative.
func filterSchemasForPermission(schemas []core.ToolSchema, mode PermissionMode) []core.ToolSchema {
	mode = NormalizePermissionMode(string(mode))
	filtered := make([]core.ToolSchema, 0, len(schemas))
	for _, schema := range schemas {
		if modeAllowsSchema(mode, schema.Name) {
			filtered = append(filtered, schema)
		}
	}
	return filtered
}

func modeAllowsSchema(mode PermissionMode, name string) bool {
	if perm.IsSafeTool(name) || name == tool.ToolSendMessage {
		return true
	}
	switch mode {
	case PermissionBypass, PermissionAuto:
		return true
	case PermissionAcceptEdits:
		if perm.IsEditTool(name) {
			return true
		}
	}
	return name == tool.ToolBash || name == tool.ToolSkill
}

func newAgentToolSet(mcpGetter func() []core.ToolSchema) *tool.Set {
	return &tool.Set{MCP: mcpGetter, IsAgent: true}
}

// generateShortID creates a short random hex ID for background tasks.
func generateShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
