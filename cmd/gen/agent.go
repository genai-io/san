package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/subagent"
)

var agentRunOpts struct {
	agentType string
	prompt    string
	model     string
	maxSteps  int
}

func init() {
	agentRunCmd.Flags().StringVar(&agentRunOpts.agentType, "type", "", "Agent type to run")
	agentRunCmd.Flags().StringVar(&agentRunOpts.prompt, "prompt", "", "Task prompt")
	agentRunCmd.Flags().StringVar(&agentRunOpts.model, "model", "", "Model override")
	agentRunCmd.Flags().IntVar(&agentRunOpts.maxSteps, "max-steps", 100, "Maximum LLM inference steps")

	agentCmd.AddCommand(agentRunCmd)
	rootCmd.AddCommand(agentCmd)
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Agent management commands",
}

var agentRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a headless agent",
	Long: `Run an agent in headless mode without TUI.

Example:
  gen agent run --type Explore --prompt "find main.go"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if agentRunOpts.agentType == "" {
			return fmt.Errorf("--type is required")
		}
		if agentRunOpts.prompt == "" {
			return fmt.Errorf("--prompt is required")
		}
		return runHeadlessAgent()
	},
}

// runHeadlessAgent executes an agent in headless mode (no TUI).
func runHeadlessAgent() error {
	cwd, _ := os.Getwd()

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down agent...")
		cancel()
	}()

	// Initialize provider
	store, _ := llm.NewStore()
	if store == nil {
		return fmt.Errorf("no provider store available")
	}

	currentModel := store.GetCurrentModel()
	var llmProvider llm.Provider

	if currentModel != nil {
		p, err := llm.GetProvider(ctx, currentModel.Provider, currentModel.AuthMethod)
		if err != nil {
			return fmt.Errorf("failed to connect provider: %w", err)
		}
		llmProvider = p
	}
	if llmProvider == nil {
		return fmt.Errorf("no provider available")
	}

	modelID := ""
	if currentModel != nil {
		modelID = currentModel.ModelID
	}
	if agentRunOpts.model != "" {
		modelID = agentRunOpts.model
	}

	// Initialize agent registry — loads built-ins and any user/project AGENT.md.
	if err := subagent.Initialize(subagent.Options{CWD: cwd}); err != nil {
		return fmt.Errorf("failed to initialize agent registry: %w", err)
	}
	if _, ok := subagent.Default().Get(agentRunOpts.agentType); !ok {
		return fmt.Errorf("unknown agent type: %s", agentRunOpts.agentType)
	}

	// Run through the full subagent pipeline so headless invocations get the
	// same permission gate (deny_tools / bypass-immune / allow_tools / mode)
	// as TUI-spawned subagents.
	executor := subagent.NewExecutor(llmProvider, cwd, modelID, nil)
	executor.SetContext(setting.IsGitRepo(cwd))

	fmt.Printf("Agent: %s\n", agentRunOpts.agentType)
	fmt.Printf("Prompt: %s\n", agentRunOpts.prompt)
	fmt.Println("---")

	req := subagent.AgentRequest{
		Agent:    agentRunOpts.agentType,
		Prompt:   agentRunOpts.prompt,
		Model:    agentRunOpts.model,
		MaxSteps: agentRunOpts.maxSteps,
		OnProgress: func(msg string) {
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
