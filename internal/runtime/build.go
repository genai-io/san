package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/infra/filecache"
	"github.com/genai-io/san/internal/infra/proc"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// BuildParams contains all values needed to construct a core.Agent.
// The app layer assembles this from env, services, and workspace state.
type BuildParams struct {
	SessionID      string
	Provider       llm.Provider
	ModelID        string
	MaxTokens      int
	ThinkingEffort string

	CWD     string
	CWDFunc func() string // dynamic CWD for tool execution; falls back to CWD if nil

	// Stream timeout tuning. Zero values (default) use the core defaults:
	// FirstChunkTimeout = 5m, IdleTimeout = 3m.
	StreamFirstChunkTimeout time.Duration
	StreamIdleTimeout       time.Duration

	// AgentCatalog provides discovery summaries of enabled predefined agents.
	// Full agent prompts stay in the worker's own context.
	AgentCatalog func() string

	// Persona overrides the system-prompt parts (identity / behavior / rules)
	// from the active persona. Empty fields keep San's built-in defaults.
	Persona system.Persona

	DisabledTools map[string]bool
	MCPTools      []core.Tool
	NativeTools   []core.Tool     // host-owned tools, subject to the same permission gate
	Tasks         *task.RunSet    // nil retains the native default
	Tracker       *todo.Store     // nil leaves the task-tracker tools unavailable
	Skills        *skill.Registry // nil retains the native default
	Plugins       *plugin.Registry
	PluginRoot    func() string // optional session-owned invocation scope

	AutoMemory AutoMemoryConfig

	// ShellDirHelp overrides cwd guidance without changing execution behavior.
	// Empty keeps the default for hosts that apply directory changes.
	ShellDirHelp string

	// ExtraTools are caller-built conditional tool schemas appended to the
	// toolset (e.g. the self-learning Evolve trigger, injected only when
	// self-learning is active). Nil ⇒ no extra tools.
	ExtraTools []core.ToolSchema

	// PermissionRules and PermissionJudge are the two stages of the
	// pre-execution permission gate: the rules stage applies the static rules
	// (permit/reject/prompt); the judge stage is the LLM judge consulted
	// only on a gray-zone prompt (AutoPilot.Permission). CanApproveByHook
	// guards the way past both: it vets a PreToolUse hook's "allow" against the
	// settings, so a hook cannot waive a deny rule, the circuit breaker, a
	// confirmation tier or an explicit ask rule. Nil fails closed.
	PermissionRules  PermDecisionFunc
	PermissionJudge  PermJudgeFunc
	CanApproveByHook PermHookApprovalFunc

	// ResultFilter may replace a tool result before the model is told about it — an
	// output too large for the window, kept elsewhere and referenced. Nil
	// sends every result through as it came.
	ResultFilter core.ResultFilter

	HookEngine   hook.Handler
	AskUser      tool.AskUserFunc
	ToolActivity func(toolCallID string, msg string)
	// BashPromptResponder answers prompts a command raises *while it runs*
	// (AutoPilot.BashPrompt plus the masked secret input) — a separate concern
	// from the pre-execution gate above.
	BashPromptResponder tool.BashPromptResponderProvider

	// OnEvent observes every agent lifecycle event synchronously, alongside
	// outbox delivery. Used by the trace writer; nil leaves recording off.
	OnEvent func(core.Event)
	// Recording owns native runtime and utility-call records. Application hosts
	// always bind it; unrecorded embedders and focused tests may leave it nil.
	Recording *session.Session
	recorder  *session.Recorder
	resuming  bool

	// FileCache tracks the files the session touched; an automatic compaction
	// restores the most recent ones. Nil restores none.
	FileCache *filecache.Cache
	// GroupRoster supplies the main session's current group reminder, if any.
	GroupRoster func() string
	// FileViews tracks read versions for Read-before-Edit/Write checks, not content.
	// Hosts retain it across runtime rebuilds; nil uses the TUI's default views.
	FileViews *fs.FileViews

	// ToolResponse receives native ToolResult.HookResponse payloads, not model
	// output or SDK Details, synchronously before ToolEnd. Hosts retain them by
	// call ID until PostTool. Must be concurrency-safe; nil uses the TUI store.
	ToolResponse func(callID string, response any)
}

// System returns the system prompt these params build. Exported so a caller
// can inspect the prompt — its size, its sections — before any session exists;
// buildAgent renders from this same function, so the two cannot drift.
func (p BuildParams) System() core.System {
	return system.Build(core.ScopeMain,
		system.WithPersona(p.Persona),
		system.WithEnvironment(system.Environment{Cwd: p.CWD, Shell: proc.DefaultShellName()}),
	)
}

// Schemas returns the built-in tool definitions these params produce: the
// standard set after the disabled-tools filter, plus any conditional extras.
// MCP tools are not included — they arrive as ready-made core.Tools rather
// than schemas, and are wired in separately.
func (p BuildParams) Schemas() []core.ToolSchema {
	return (&tool.Set{
		Disabled:     p.DisabledTools,
		AgentCatalog: p.AgentCatalog,
		ShellDirHelp: p.ShellDirHelp,
		ExtraTools:   p.ExtraTools,
	}).Tools()
}

// ToolSchemas returns the tool definitions an agent built from p sends, MCP
// tools included, in the order it sends them.
func (p BuildParams) ToolSchemas() []core.ToolSchema {
	tools := tool.AdaptToolRegistry(p.Schemas(), func() string { return p.CWD })
	p.addTools(tools)
	return tools.Schemas()
}

func (p BuildParams) addTools(tools *core.Tools) {
	for _, t := range p.NativeTools {
		if !p.DisabledTools[t.Schema().Name] {
			tools.Add(t, "native:"+t.Schema().Name)
		}
	}
	for _, t := range p.MCPTools {
		if p.DisabledTools[t.Schema().Name] {
			continue
		}
		tools.Add(t, "mcp:"+t.Schema().Name)
	}
}

type buildOption func(*core.Config)

func withoutOutbox() buildOption {
	return func(cfg *core.Config) { cfg.OutboxBuf = -1 }
}

func withRequestContext(appendContext func([]core.Message, bool) ([]core.Message, func() core.Event)) buildOption {
	return func(cfg *core.Config) { cfg.Inference.AppendContext = appendContext }
}

func buildAgent(p BuildParams, opts ...buildOption) (core.Agent, *PermissionGate, error) {
	if p.Provider == nil {
		return nil, nil, fmt.Errorf("no LLM provider configured")
	}
	if p.Recording != nil && p.PermissionRules == nil {
		return nil, nil, fmt.Errorf("typed runtime requires a permission policy")
	}
	if err := p.prepareRecording(); err != nil {
		return nil, nil, err
	}

	client := llm.NewClient(p.Provider, p.ModelID, p.MaxTokens)
	client.SetThinkingEffort(p.ThinkingEffort)

	sys := p.System()

	cwdFunc := p.CWDFunc
	if cwdFunc == nil {
		cwd := p.CWD
		cwdFunc = func() string { return cwd }
	}

	schemas := p.Schemas()
	var adaptOpts []tool.AdaptOption
	toolContext := func(ctx context.Context) context.Context {
		ctx = tool.WithMessageSource(ctx, "", 0)
		if p.Recording != nil {
			snapshot := p.Recording.Snapshot()
			if source, ok := snapshot.ToolCallSource(core.ToolCallIDFromContext(ctx)); ok {
				ctx = tool.WithMessageSource(ctx, source.Session, source.Seq)
			}
		}
		if p.Tasks != nil {
			ctx = task.WithRunSet(task.WithSessionID(ctx, p.SessionID), p.Tasks)
		}
		if p.Plugins != nil {
			ctx = plugin.WithRegistry(ctx, p.Plugins)
			if p.PluginRoot != nil {
				ctx = plugin.WithRoot(ctx, p.PluginRoot())
			}
			ctx = fs.WithShellEnvProvider(ctx, plugin.PluginEnv)
		}
		if p.Skills != nil {
			ctx = skill.WithRegistry(ctx, p.Skills)
		}
		if p.Tracker != nil {
			ctx = todo.WithStore(ctx, p.Tracker)
		}
		if p.FileViews != nil {
			return fs.WithFileViews(ctx, p.FileViews)
		}
		return ctx
	}
	adaptOpts = append(adaptOpts, tool.WithToolContext(toolContext))
	if p.ToolResponse != nil {
		adaptOpts = append(adaptOpts, tool.WithSideEffectHandler(p.ToolResponse))
	}
	if p.AskUser != nil {
		adaptOpts = append(adaptOpts, tool.WithAskUser(p.AskUser))
	}
	if p.ToolActivity != nil {
		adaptOpts = append(adaptOpts, tool.WithToolActivity(p.ToolActivity))
	}
	if p.BashPromptResponder != nil {
		adaptOpts = append(adaptOpts, tool.WithBashPromptResponderProvider(p.BashPromptResponder))
	}
	pg := NewPermissionGate(p.PermissionRules)
	pg.SetJudge(p.PermissionJudge)
	pg.SetHookAllowCheck(p.CanApproveByHook)
	disabledTools, hasPermissionRules := p.DisabledTools, p.PermissionRules != nil
	adaptOpts = append(adaptOpts, tool.WithPermissionCheck(func(ctx context.Context, name string, input map[string]any) (bool, string) {
		if disabledTools[name] || !hasPermissionRules {
			return false, "agent execution is unavailable"
		}
		return pg.Check(ctx, name, input, false, "")
	}))
	var ag core.Agent
	adaptOpts = append(adaptOpts, tool.WithMessagesGetterProvider(func() []core.Message {
		if ag == nil {
			return nil
		}
		return ag.Messages()
	}))
	tools := tool.AdaptToolRegistry(schemas, cwdFunc, adaptOpts...)
	p.addTools(tools)

	compactClient := client
	cache := p.FileCache
	compactFunc := func(ctx context.Context, msgs []core.Message) (core.Compaction, error) {
		scope := p.Recording.Completion(session.PurposeCompaction)
		result := scope.Compaction("")
		text := core.BuildCompactionText(msgs)
		resp, err := compactClient.Complete(ctx, system.CompactPrompt(), []core.Message{core.UserMessage(text, nil)}, nil, core.CompactMaxTokens, scope.Admission())
		if err != nil {
			return result, err
		}
		summary := strings.TrimSpace(resp.Content.Text())
		if summary == "" {
			return result, fmt.Errorf("compaction produced empty summary")
		}
		budget := client.ContextBudget()
		result.Summary = core.WithRecentRequests(summary, msgs, budget)
		result.Reminders = postCompactReminders(cwdFunc(), cache, budget)
		if p.Skills != nil {
			result.Reminders = append(result.Reminders, reminder.WrapWithSource(p.Skills.PromptSection(), reminder.ProviderSkillsDirectory))
		}
		if p.GroupRoster != nil {
			result.Reminders = append(result.Reminders, reminder.WrapWithSource(p.GroupRoster(), reminder.ProviderGroup))
		}
		return result, nil
	}

	permissionGate := tool.HookedPermission(p.HookEngine, pg)
	if p.recorder != nil {
		permissionGate = tool.HookedPermission(p.HookEngine, pg, p.recorder.RecordPermission)
	}
	if err := p.recordCatalogues(sys, tools); err != nil {
		pg.Close()
		return nil, nil, err
	}
	cfg := core.Config{
		ID: "main",
		Inference: core.InferenceConfig{
			ResolveClient: client.TurnClient,
			AdaptMessages: client.RequestMessages,
			Options:       client.CallOptions,
		},
		Compaction: core.CompactionConfig{
			Budget:    client.ContextBudget,
			Summarize: compactFunc,
		},
		System: sys,
		Tools:  tools,
		Gate: func(ctx context.Context, call sdkagent.PreToolContext) (sdkagent.Decision, error) {
			return permissionGate(core.WithToolCallID(toolContext(ctx), call.Call.ID), call)
		},
		ResultFilter: p.ResultFilter,
		OnEvent:      p.OnEvent,

		StreamFirstChunkTimeout: p.StreamFirstChunkTimeout,
		StreamIdleTimeout:       p.StreamIdleTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if p.recorder != nil {
		cfg.InferenceAdmission = p.recorder.InferenceAdmission
		cfg.ToolAdmission = p.recorder.ToolAdmission
		cfg.OutputAdmission = p.recorder.OutputAdmission
	}
	ag = core.NewAgent(cfg)
	if p.recorder != nil {
		if err := p.recorder.Err(); err != nil {
			pg.Close()
			return nil, nil, &llm.RecordingError{Err: err}
		}
	}

	return ag, pg, nil
}

// MemoryRecaller uses the main model and the configured Wait budget.
func (p BuildParams) MemoryRecaller() Recaller {
	r := Recaller{Provider: p.Provider, Model: p.ModelID, Effort: LowestEffort(p.Provider, nil, &llm.CurrentModelInfo{ModelID: p.ModelID}), Recording: p.Recording, IndexLimits: p.AutoMemory.IndexLimits}
	if p.AutoMemory.Recall.Mode == setting.MemoryRecallWait {
		r.Timeout = p.AutoMemory.Recall.Timeout
		if r.Timeout <= 0 {
			r.Timeout = 5 * time.Second
		}
	}
	return r
}

// instructionReminders reads the same user and project instructions as the TUI.
func instructionReminders(cwd string) []string {
	user, project := system.FormatInstructions(system.LoadMemoryFiles(cwd))
	return []string{
		reminder.WrapWithSource(user, reminder.ProviderMemoryUser),
		reminder.WrapWithSource(project, reminder.ProviderMemoryProject),
	}
}

// postCompactReminders is what an automatic compaction re-attaches: the
// instructions, and the files most recently read or edited when files is set —
// as many as fit in a quarter of a known budget, so compaction still shrinks.
func postCompactReminders(cwd string, files *filecache.Cache, budget int) []string {
	reminders := instructionReminders(cwd)
	if files == nil {
		return reminders
	}
	restored, _ := files.RestoreRecent()
	if budget > 0 {
		room := budget // bytes: a quarter of the budget's tokens at ~4 bytes each
		for i, f := range restored {
			room -= len(f.Content)
			if room < 0 {
				restored = restored[:i]
				break
			}
		}
	}
	if len(restored) > 0 {
		reminders = append(reminders, reminder.WrapWithSource(filecache.FormatRestoredFiles(restored), reminder.SourceRestoredFiles))
	}
	return reminders
}
