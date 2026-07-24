package main

import (
	"github.com/spf13/cobra"

	"github.com/genai-io/san/internal/app"
)

var agentRunOpts app.AgentRunOptions

func init() {
	agentRunCmd.Flags().StringVar(&agentRunOpts.Name, "name", "", "Optional custom agent name")
	agentRunCmd.Flags().StringVar(&agentRunOpts.Prompt, "prompt", "", "Task prompt")
	agentRunCmd.Flags().StringVar(&agentRunOpts.Model, "model", "", "Model override")
	agentRunCmd.Flags().IntVar(&agentRunOpts.MaxSteps, "max-steps", 500, "Maximum LLM inference steps (minimum 500)")

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
	Long: `Run a subagent in headless mode without TUI.

Example:
  san agent run --prompt "find main.go"
  san agent run --name test-runner --prompt "run the tests"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.RunAgent(agentRunOpts)
	},
}
