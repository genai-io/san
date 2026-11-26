package main

import (
	"github.com/spf13/cobra"

	"github.com/genai-io/san/internal/app/cli"
)

var agentRunOpts cli.AgentRunOptions

func init() {
	agentRunCmd.Flags().StringVar(&agentRunOpts.Name, "name", "", "Optional agent name")
	agentRunCmd.Flags().StringVar(&agentRunOpts.Prompt, "prompt", "", "Task prompt")
	agentRunCmd.Flags().StringVar(&agentRunOpts.Model, "model", "", "Model override")
	agentRunCmd.Flags().IntVar(&agentRunOpts.MaxSteps, "max-steps", 100, "Maximum LLM inference steps")

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
  san agent run --prompt "find main.go"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := agentRunOpts.Validate(); err != nil {
			return err
		}
		warnShellFallback()
		return cli.RunAgent(agentRunOpts)
	},
}
