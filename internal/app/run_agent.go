// Headless agent entry point: san agent run. Runs a single subagent without the
// TUI, sharing provider resolution (resolveProvider) with print mode and the
// full subagent pipeline (permission gating, mode) with TUI-spawned agents.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// AgentRunOptions configures a one-shot headless agent run.
type AgentRunOptions struct {
	Name     string // optional agent name
	Prompt   string // task prompt (required)
	Model    string // model override; empty uses the connected provider's model
	MaxSteps int    // maximum LLM inference steps
}

// newHeadlessExecutor installs native persistence and the current headless
// policy before any worker can be admitted. A nil parent is a top-level run.
func newHeadlessExecutor(provider llm.Provider, cwd, model string, settings *setting.Data, store *session.Store, registry *subagent.Registry, tasks *task.Manager) (*subagent.Executor, error) {
	executor := subagent.NewExecutor(provider, cwd, model, nil)
	if err := executor.SetSessions(store, nil); err != nil {
		return nil, err
	}
	permissions := &setting.SessionPermissions{ShouldAvoidPrompts: true}
	permissions.AddWorkingDirectory(cwd)
	executor.ConfigureOwner(tasks, registry, func(_ *session.Session, _ bool, source tool.AgentInteractionSource) tool.Resolver {
		cwd := source.WorkingDirectory
		policy := agent.PermissionPolicy{Settings: setting.ScopedPermissions{Data: settings, CWD: cwd}, Session: permissions}
		return headlessPermissionResolver{policy: policy}
	}, nil)
	executor.SetDisabledTools(setting.WithDefaultDisabledTools(settings.DisabledTools))
	executor.SetChildHooks(func(owner *session.Session, provider llm.Provider, model string) (hook.Handler, func(llm.Provider, string), func()) {
		projection := owner.Snapshot()
		engine := hook.NewEngine(settings, owner.ID(), projection.Identity().InitialCWD, owner.Path())
		bindSessionHooks(engine, provider, owner, model)
		return engine, func(provider llm.Provider, model string) { bindSessionHooks(engine, provider, owner, model) }, engine.Shutdown
	})
	executor.SetPostTool(func(handler hook.Handler, result core.ToolResult, effect any) error {
		engine, _ := handler.(*hook.Engine)
		agent.PostTool(result, effect, engine, agent.WorkspaceCallbacks{})
		return nil
	})
	return executor, nil
}

type headlessPermissionResolver struct{ policy agent.PermissionPolicy }

func (r headlessPermissionResolver) Resolve(ctx context.Context, name string, input map[string]any, force bool, reason string) perm.Resolution {
	if err := ctx.Err(); err != nil {
		return perm.Resolution{Decision: "cancelled", By: "config", Reason: err.Error()}
	}
	decision := r.policy.Decide(name, input)
	if decision.Decision == perm.Permit && !force {
		return perm.Resolution{Decision: "allow", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
	}
	if decision.Decision == perm.Reject {
		return perm.Resolution{Decision: "deny", By: decision.By, Scope: decision.Scope, Reason: decision.Reason}
	}
	return perm.Resolution{Decision: "deny", By: "config", Reason: "headless agent cannot ask for approval: " + reason}
}

// RunAgent executes a single agent in headless mode (no TUI).
func RunAgent(opts AgentRunOptions) (runErr error) {
	if opts.Prompt == "" {
		return fmt.Errorf("--prompt is required")
	}
	warnShellFallback()

	// Graceful shutdown: SIGINT/SIGTERM cancels the run's context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down agent...")
			cancel()
		case <-ctx.Done():
		}
	}()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	store, err := session.OpenStore(ctx, cwd)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	provider, modelID, err := resolveProvider(ctx)
	if err != nil {
		return err
	}
	if opts.Model != "" {
		modelID = opts.Model
	}

	warnUntrustedWorkspace(cwd)
	workerSettings, err := setting.LoadForCwd(cwd)
	if err != nil {
		return fmt.Errorf("failed to load agent settings: %w", err)
	}

	// Initialize agent registry from user/project definitions.
	if err := subagent.Initialize(subagent.Options{CWD: cwd}); err != nil {
		return fmt.Errorf("failed to initialize agent registry: %w", err)
	}
	if opts.Name != "" {
		if _, ok := subagent.Default().Get(opts.Name); !ok {
			return fmt.Errorf("unknown agent: %s", opts.Name)
		}
	}

	// Run through the full subagent pipeline so headless invocations get the
	// same permission gate (deny_tools / confirmation floor / allow_tools / mode)
	// as TUI-spawned subagents.
	pool := task.NewManager()
	defer func() { runErr = errors.Join(runErr, pool.StopServices("")); pool.Cancel(); pool.Join() }()
	executor, err := newHeadlessExecutor(provider, cwd, modelID, workerSettings, store, subagent.Default(), pool)
	if err != nil {
		return err
	}
	executor.SetDefaultModel(workerSettings.SubagentModel)
	executor.SetResolver(llm.NewProviderPool(llm.Default().Store()))

	if opts.Name != "" {
		fmt.Printf("Agent: %s\n", opts.Name)
	}
	fmt.Printf("Prompt: %s\n", opts.Prompt)
	fmt.Println("---")

	req := tool.AgentExecRequest{
		Agent:    opts.Name,
		Prompt:   opts.Prompt,
		Model:    opts.Model,
		MaxSteps: opts.MaxSteps,
		OnActivity: func(msg string) {
			fmt.Fprintln(os.Stderr, "·", msg)
		},
	}
	result, err := executor.Run(ctx, req)
	if result != nil {
		if result.Content != "" {
			fmt.Println(result.Content)
		}
		fmt.Printf("\n---\nDone: %d steps, %d tool uses (success=%t)\n", result.StepCount, result.ToolUses, result.Success)
		if result.TranscriptPath != "" {
			fmt.Printf("Transcript: %s\n", result.TranscriptPath)
		}
		if result.Error != "" {
			fmt.Printf("Error: %s\n", result.Error)
		}
	}
	if err != nil {
		return fmt.Errorf("agent failed: %w", err)
	}
	return nil
}
