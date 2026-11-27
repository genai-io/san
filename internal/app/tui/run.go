package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
)

// Run starts the interactive terminal.
func Run(opts setting.RunOptions) (runErr error) {
	var m *model
	// Stop the runtime before its services, and close its session last.
	defer func() {
		tasks := task.Default()
		if m != nil {
			m.userInput.Session.Selector.Cancel()
			if err := m.StopAgentRuntime(); err != nil {
				// The live run still owns its services and transcript writer.
				runErr = errors.Join(runErr, err)
				return
			}
			m.closeUserPrompts()
			tasks = m.features.Task
		}
		if err := tasks.Shutdown(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if m != nil {
			m.resetRecap()
			runErr = errors.Join(runErr, m.closeSessionRecording())
		}
	}()

	if userQuit, err := widgets.ResolveTheme(setting.LoadTheme(), setting.SaveTheme); userQuit || err != nil {
		return err
	}
	if cwd, err := os.Getwd(); err == nil {
		if userQuit, err := resolveWorkspaceTrust(cwd); userQuit || err != nil {
			return err
		}
	}

	var err error
	m, err = initModel(opts)
	if err != nil {
		return err
	}

	// Fresh sessions show the splash live above the input from launch and freeze
	// it into scrollback on the first commit, so it stays visible immediately
	// yet reflects the model the user picks after launch instead of freezing
	// "no model selected" into scrollback. Resumed sessions skip it —
	// commitAllMessages will reprint the conversation immediately, so a splash
	// would just be churn.
	if len(m.conv.Messages) == 0 {
		m.welcomePending = true
	}

	m.flush.output = &terminalOutput{writer: os.Stdout}
	finalModel, err := tea.NewProgram(m, tea.WithOutput(m.flush.output)).Run()
	if outputErr := m.flush.output.Flush(); err == nil {
		err = outputErr
	}
	// Every exit ends here, a failed one included: settle the group
	// membership before anything else.
	m.exitGroup()
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
	if m.asyncHookQueue == nil {
		return
	}
	queue := m.asyncHookQueue
	m.features.Hook.SetAsyncHookCallback(func(result hook.AsyncHookResult) {
		reason := result.BlockReason
		if reason == "" {
			reason = "asynchronous hook requested a rewake"
		}
		queue.push(asyncHookRewake{
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
	if outcome.AdditionalContext != "" {
		m.features.Reminder.EnqueueWithSource(outcome.AdditionalContext, reminder.SourceSessionStart)
	}
}

func printExitMessage(m *model) {
	command := ""
	if owner := m.features.Session; owner != nil {
		history := owner.Snapshot()
		if history.HasMessages() {
			saved := ""
			if cwd, _ := os.Getwd(); cwd == "" {
				saved = history.Identity().InitialCWD
			} else if _, err := resumeDirectory(history, cwd); err != nil {
				saved = history.ExecutionCWD()
			}
			command = resumeCommandForSession(owner.ID(), owner.Path(), saved)
		}
	}
	if command != "" {
		dim := widgets.DimStyle()
		fmt.Println()
		fmt.Println(dim.Render("Resume this session with:"))
		fmt.Println(dim.Render(command))
		fmt.Println()
	}
	// The one auto-update outcome the user should hear about, and the one
	// moment it cannot interrupt anything.
	if f := m.updateFailed; f.version != "" {
		warn := lipgloss.NewStyle().Foreground(widgets.CurrentTheme.Warning)
		fmt.Println(warn.Render(fmt.Sprintf("Auto-update to v%s failed: %v", f.version, f.err)))
		fmt.Println(warn.Render("Run `san update` to retry."))
		fmt.Println()
	}
}

func resumeCommandForSession(sessionID, transcriptPath, savedCWD string) string {
	if sessionID == "" || transcriptPath == "" {
		return ""
	}
	if _, err := os.Stat(transcriptPath); err != nil {
		return ""
	}
	command := "san -r " + sessionID
	if cwd, _ := os.Getwd(); savedCWD != "" && savedCWD != cwd {
		return "cd '" + strings.ReplaceAll(savedCWD, "'", `'\''`) + "' && " + command
	}
	return command
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
