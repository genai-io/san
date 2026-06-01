// Package app provides the unified entry point for interactive and non-interactive modes.
package app

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/app/trigger"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/tool"
)

// Run routes to either print mode or interactive TUI.
func Run(opts setting.RunOptions) error {
	if opts.Print != "" {
		return runPrint(opts.Print)
	}

	if userQuit, err := kit.ResolveTheme(setting.LoadTheme(), setting.SaveTheme); userQuit || err != nil {
		return err
	}

	m, err := initModel(opts)
	if err != nil {
		return err
	}

	// Fresh sessions get the splash screen before Bubbletea takes over.
	// Resumed sessions skip it — commitAllMessages will reprint the
	// conversation immediately, so a splash would just be churn.
	if len(m.conv.Messages) == 0 {
		printWelcome(welcomeInfo{
			Model: m.services.LLM.ModelID(),
			CWD:   m.env.CWD,
		})
	}

	finalModel, err := tea.NewProgram(m, tea.WithMouseCellMotion()).Run()
	if err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	if fm, ok := finalModel.(*model); ok {
		printExitMessage(fm)
	}
	return nil
}

func initModel(opts setting.RunOptions) (*model, error) {
	if err := initInfrastructure(); err != nil {
		return nil, err
	}
	m, err := newModel(opts)
	if err != nil {
		return nil, err
	}
	m.fireStartupHooks()
	return m, nil
}

func (m *model) configureAsyncHookCallback() {
	if m.services.Hook == nil || m.systemInput.AsyncHookQueue == nil {
		return
	}
	queue := m.systemInput.AsyncHookQueue
	m.services.Hook.SetAsyncHookCallback(func(result hook.AsyncHookResult) {
		reason := result.BlockReason
		if reason == "" {
			reason = "asynchronous hook requested a rewake"
		}
		queue.Push(trigger.AsyncHookRewake{
			Notice:             fmt.Sprintf("Async hook blocked: %s", reason),
			Context:            []string{formatAsyncHookContinuationContext(result, reason)},
			ContinuationPrompt: "A background policy hook reported a blocking condition. Re-evaluate the plan and choose a safer next step.",
		})
	})
}

func (m *model) fireStartupHooks() {
	outcome := m.executeStartupHooks(context.Background())
	m.applyStartupHookOutcome(outcome)
	// Hook-injected context rides on the same harness channel as skills and
	// memory: it gets queued for the first user message as a
	// <system-reminder>, not appended as a standalone user message. This
	// keeps SessionStart context out of the visible chat and lets it
	// re-emerge after PostCompact alongside other harness reminders.
	if outcome.AdditionalContext != "" && m.services.Reminder != nil {
		m.services.Reminder.Enqueue(outcome.AdditionalContext)
	}
}

func printExitMessage(m *model) {
	sessionID := m.services.Session.ID()
	command := resumeCommandForSession(sessionID, m.services.Session.TranscriptPath())
	if command != "" {
		dim := kit.DimStyle()
		fmt.Println()
		fmt.Println(dim.Render("Resume this session with:"))
		fmt.Println(dim.Render(command))
		fmt.Println()
	}
}

func resumeCommandForSession(sessionID, transcriptPath string) string {
	if sessionID == "" || transcriptPath == "" {
		return ""
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		return ""
	}
	return "gen -r " + sessionID
}

func formatAsyncHookContinuationContext(result hook.AsyncHookResult, reason string) string {
	return fmt.Sprintf(
		"<background-hook-result>\nstatus: blocked\nevent: %s\nhook_type: %s\nhook_source: %s\nhook_name: %s\nreason: %s\ninstruction: Re-evaluate the plan before any further model or tool action.\n</background-hook-result>",
		result.Event,
		result.HookType,
		result.HookSource,
		result.HookName,
		reason,
	)
}

func runPrint(userMessage string) error {
	ctx := context.Background()

	store, err := llm.NewStore()
	if err != nil {
		return fmt.Errorf("failed to load store: %w", err)
	}

	var llmProvider llm.Provider
	var modelID string

	current := store.GetCurrentModel()
	if current != nil {
		p, err := llm.GetProvider(ctx, current.Provider, current.AuthMethod)
		if err != nil {
			return fmt.Errorf("provider %s (%s) not available: %w. Run 'gen' and use /model to connect",
				current.Provider, current.AuthMethod, err)
		}
		llmProvider = p
		modelID = current.ModelID
	} else {
		for providerName, conn := range store.GetConnections() {
			p, err := llm.GetProvider(ctx, llm.Name(providerName), conn.AuthMethod)
			if err == nil {
				llmProvider = p
				modelID = setting.DefaultModel(providerName, string(conn.AuthMethod))
				break
			}
		}
	}

	if llmProvider == nil {
		return fmt.Errorf("no provider connected. Run 'gen' and use /model to connect")
	}

	completionOpts := llm.CompletionOptions{
		Model:        modelID,
		MaxTokens:    setting.DefaultMaxTokens,
		SystemPrompt: setting.DefaultSystemPrompt,
		Messages:     []core.Message{core.UserMessage(userMessage, nil)},
		Tools:        tool.GetToolSchemas(),
	}

	streamChan := llmProvider.Stream(ctx, completionOpts)
	for chunk := range streamChan {
		switch chunk.Type {
		case llm.ChunkTypeText:
			fmt.Print(chunk.Text)
		case llm.ChunkTypeError:
			return chunk.Error
		case llm.ChunkTypeDone:
			fmt.Println()
		}
	}

	return nil
}
