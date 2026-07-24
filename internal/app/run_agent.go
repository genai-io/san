// Headless agent entry point: san agent run. Runs a single subagent without the
// TUI, sharing provider resolution (resolveProvider) with print mode and the
// full subagent pipeline (permission gating, mode) with TUI-spawned agents.
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
)

// AgentRunOptions configures a one-shot headless agent run.
type AgentRunOptions struct {
	Name     string // optional custom agent name; empty uses the default agent
	Prompt   string // task prompt (required)
	Model    string // model override; empty uses the connected provider's model
	MaxSteps int    // maximum LLM inference steps
}

// RunAgent executes a single agent in headless mode (no TUI).
func RunAgent(opts AgentRunOptions) error {
	if opts.Prompt == "" {
		return fmt.Errorf("--prompt is required")
	}

	// Graceful shutdown: SIGINT/SIGTERM cancels the run's context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down agent...")
		cancel()
	}()

	resolved, store, err := resolveProviderWithStore(ctx)
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()

	if err := plugin.Initialize(ctx, plugin.Options{CWD: cwd}); err != nil {
		return fmt.Errorf("failed to initialize plugins: %w", err)
	}
	skill.Initialize(skill.Options{CWD: cwd, PluginSkillPaths: pluginSkillPaths})
	if err := subagent.Initialize(subagent.Options{CWD: cwd, PluginAgentPaths: pluginAgentPaths}); err != nil {
		return fmt.Errorf("failed to initialize subagent registry: %w", err)
	}
	if err := mcp.Initialize(mcp.Options{CWD: cwd, PluginServers: pluginMCPServers}); err != nil {
		return fmt.Errorf("failed to initialize MCP registry: %w", err)
	}
	fs.SetEnvProvider(plugin.PluginEnv)

	// Run through the full subagent pipeline so headless invocations get the
	// same permission gate (deny_tools / confirmation floor / allow_tools / mode)
	// as TUI-spawned subagents.
	executor := subagent.NewExecutor(resolved.Provider, cwd, resolved.ModelID, nil)
	executor.SetResolver(llm.NewProviderPool(store))
	executor.SetModelStore(store, resolved.ProviderName, resolved.AuthMethod)
	executor.SetSkillsDirectory(skill.Default().PromptSection())
	executor.SetMCP(mcp.DefaultRegistry(), mcp.DefaultRegistry())

	name := opts.Name
	if name == "" {
		name = "subagent"
	}
	fmt.Printf("Agent: %s\n", name)
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
	if err != nil {
		return fmt.Errorf("agent failed: %w", err)
	}

	if result.Content != "" {
		fmt.Println(result.Content)
	}

	fmt.Printf("\n---\nDone: %d steps, %d tool uses (success=%t)\n", result.StepCount, result.ToolUses, result.Success)
	if result.Error != "" {
		fmt.Printf("Error: %s\n", result.Error)
	}
	return nil
}
