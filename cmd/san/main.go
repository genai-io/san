package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/genai-io/san/internal/app"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"

	// Import providers for registration
	_ "github.com/genai-io/san/internal/llm/alibaba"
	_ "github.com/genai-io/san/internal/llm/anthropic"
	_ "github.com/genai-io/san/internal/llm/bigmodel"
	_ "github.com/genai-io/san/internal/llm/deepseek"
	_ "github.com/genai-io/san/internal/llm/google"
	_ "github.com/genai-io/san/internal/llm/mimo"
	_ "github.com/genai-io/san/internal/llm/minmax"
	_ "github.com/genai-io/san/internal/llm/moonshot"
	_ "github.com/genai-io/san/internal/llm/ollama"
	_ "github.com/genai-io/san/internal/llm/openai"
	_ "github.com/genai-io/san/internal/llm/sensenova"
	_ "github.com/genai-io/san/internal/llm/volcengine"
)

var version = "1.20.3"

// cliOpts holds all CLI flag values in one place.
var cliOpts struct {
	print  string // -p/--print: non-interactive print mode
	cont   bool   // --continue
	resume bool   // --resume

	pluginDir string
	persona   string // --persona: persona name to activate on startup
}

func init() {
	// Load .env file if it exists (silent fail if not found)
	_ = godotenv.Load()
	// Initialize logging (enabled via SAN_DEBUG=1)
	_ = log.Init()

	// Set app version for session entries.
	session.SetAppVersion(version)

	// Register flags
	rootCmd.Flags().StringVarP(&cliOpts.print, "print", "p", "", "Non-interactive print mode with prompt")
	rootCmd.Flags().BoolVarP(&cliOpts.cont, "continue", "c", false, "Resume the most recent session")
	rootCmd.Flags().BoolVarP(&cliOpts.resume, "resume", "r", false, "Select and resume a previous session")
	rootCmd.PersistentFlags().StringVar(&cliOpts.pluginDir, "plugin-dir", "", "Load plugins from a specific directory")
	rootCmd.Flags().StringVar(&cliOpts.persona, "persona", "", "Activate a persona on startup")

	// Register subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(helpCmd)
	rootCmd.SetHelpCommand(helpCmd)
	rootCmd.AddCommand(mcpCmd)
}

func main() {
	defer func() { _ = log.Sync() }()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "san [message]",
	Short: "San - AI coding assistant for the terminal",
	Long: `San is an open-source AI assistant for the terminal.
Extensible tools, customizable prompts, multi-provider support.

Non-interactive mode:
  san -p "your prompt"     Print response and exit
  echo "msg" | san -p ""   Pipe stdin in print mode`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		printPrompt := cliOpts.print
		if printPrompt == "" {
			printPrompt = readStdin()
		}

		// When -r is used with an argument, treat it as a session ID
		var resumeID string
		if cliOpts.resume && len(args) > 0 {
			resumeID = args[0]
			args = args[1:]
		}

		prompt := strings.Join(args, " ")

		opts := setting.RunOptions{
			Print:     printPrompt,
			Prompt:    prompt,
			PluginDir: cliOpts.pluginDir,
			Persona:   cliOpts.persona,
			Continue:  cliOpts.cont,
			Resume:    cliOpts.resume,
			ResumeID:  resumeID,
		}
		if err := app.Run(opts); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

// readStdin returns piped stdin data, or empty string if stdin is a terminal.
func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		reader := bufio.NewReader(os.Stdin)
		data, err := io.ReadAll(reader)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("san version %s\n", version)
	},
}

var helpCmd = &cobra.Command{
	Use:   "help",
	Short: "Show help information",
	Long:  "Display help information about San and its commands.",
	Run: func(cmd *cobra.Command, args []string) {
		printHelp()
	},
}

func printHelp() {
	help := `
San - AI coding assistant for the terminal

Usage:
  san                        Start interactive chat mode
  san "message"              Interactive mode with initial prompt
  san -p "prompt"            Non-interactive print mode
  san [command]              Run a command

Print Mode (non-interactive):
  san -p "your prompt"       Print response and exit
  echo "data" | san -p "analyze"  Pipe stdin with prompt

Interactive Mode:
  san                        Start chat
  san "Explain this code"    Start chat with initial prompt

Session:
  san -c, --continue         Resume the most recent session
  san -r, --resume           Select and resume a previous session
  san -r <session-id>        Resume a specific session by ID
  san --plugin-dir <path>    Load plugins from a specific directory
  san --persona <name>       Activate a persona on startup

Commands:
  version      Print the version number
  agent run    Run a headless agent
  help         Show this help message

Keybindings:
  Enter        Send message
  Alt+Enter    Insert newline
  Up/Down      Navigate input history
  Esc          Stop AI response
  Ctrl+T       Toggle task list display
  Ctrl+C       Clear input / Quit

Slash Commands:
  /model       Select model and manage provider connections
  /clear       Clear chat history
  /help        Show help

Examples:
  san                        Start interactive chat
  san "Explain this code"    Interactive with initial prompt
  san -p "Explain this code" Print response and exit
  san -c                     Resume previous session
  san version                Show version

For more information, visit: https://github.com/genai-io/san
`
	fmt.Println(help)
}
