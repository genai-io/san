// Hook-forwarding model methods and related helpers.
// These methods use m.services.Hook to fire lifecycle events,
// replacing the former env hook methods that used singletons directly.
package app

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
)

func (m *model) firePostToolHook(tr core.ToolResult, sideEffect any) {
	eventType := hook.PostToolUse
	if tr.IsError {
		eventType = hook.PostToolUseFailure
	}
	toolResponse := any(tr.Content)
	if sideEffect != nil {
		toolResponse = sideEffect
	}
	input := hook.HookInput{
		ToolName:     tr.ToolName,
		ToolUseID:    tr.ToolCallID,
		ToolResponse: toolResponse,
	}
	if tr.IsError {
		input.Error = tr.Content
	}
	m.services.Hook.ExecuteAsync(eventType, input)
}

func (m *model) fireStopFailureHook(lastAssistantContent string, err error) {
	m.services.Hook.ExecuteAsync(hook.StopFailure, hook.HookInput{
		LastAssistantMessage: lastAssistantContent,
		Error:                err.Error(),
		StopHookActive:       m.services.Hook.StopHookActive(),
	})
}

func (m *model) executeStartupHooks(ctx context.Context) hook.HookOutcome {
	m.services.Hook.ExecuteAsync(hook.Setup, hook.HookInput{
		Trigger: "init",
	})
	source := "startup"
	if m.services.Session.ID() != "" {
		source = "resume"
	}
	// Enqueue session-level reminders (skills directory, memory, etc.) so
	// they ride on the first user message of this session. The system-prompt
	// cache prefix stays untouched.
	m.services.Reminder.RequeueSystemReminders()
	return m.services.Hook.Execute(ctx, hook.SessionStart, hook.HookInput{
		Source: source,
		Model:  m.env.GetModelID(),
	})
}

type stopHookResultMsg struct {
	Blocked bool
	Reason  string
	Result  core.Result
}

func (m *model) fireIdleHooksCmd(result core.Result) tea.Cmd {
	hookEngine := m.services.Hook

	lastContent := core.LastAssistantChatContent(m.conv.Messages)
	hasStopHooks := hookEngine.HasHooks(hook.Stop)
	stopHookActive := hookEngine.StopHookActive()

	return func() tea.Msg {
		var blocked bool
		var reason string
		if hasStopHooks {
			outcome := hookEngine.Execute(context.Background(), hook.Stop, hook.HookInput{
				LastAssistantMessage: lastContent,
				StopHookActive:       stopHookActive,
			})
			if outcome.ShouldBlock {
				blocked = true
				reason = outcome.BlockReason
			}
		}
		hookEngine.ExecuteAsync(hook.Notification, hook.HookInput{
			Message:          "Claude is waiting for your input",
			NotificationType: "idle_prompt",
		})
		return stopHookResultMsg{Blocked: blocked, Reason: reason, Result: result}
	}
}

func (m *model) checkPromptHook(ctx context.Context, prompt string) (bool, string) {
	outcome := m.services.Hook.Execute(ctx, hook.UserPromptSubmit, hook.HookInput{Prompt: prompt})
	// UserPromptSubmit hooks may return additionalContext to inject into the
	// next turn. Enqueue as a <system-reminder> so it rides on the user
	// message that's about to go out (which is the same prompt being checked).
	if outcome.AdditionalContext != "" {
		m.services.Reminder.Enqueue(outcome.AdditionalContext)
	}
	return outcome.ShouldBlock, outcome.BlockReason
}

func (m *model) switchProvider(p llm.Provider) {
	m.env.LLMProvider = p
	m.services.Hook.SetLLMCompleter(buildHookCompleter(p), m.env.GetModelID())
}

func (m *model) refreshMemoryContext(cwd, loadReason string) {
	files := system.LoadMemoryFiles(cwd)
	var userParts, projectParts []string
	for _, f := range files {
		switch f.Level {
		case "global":
			userParts = append(userParts, f.Content)
		case "project", "local":
			projectParts = append(projectParts, f.Content)
		}
		m.services.Hook.ExecuteAsync(hook.InstructionsLoaded, hook.HookInput{
			FilePath:   f.Path,
			MemoryType: memoryTypeForLevel(f.Level),
			LoadReason: loadReason,
		})
	}
	m.env.CachedUserInstructions = joinSections(userParts)
	m.env.CachedProjectInstructions = joinSections(projectParts)
}

func (m *model) syncSettingsToHookEngine() {
	m.services.Hook.SetSettings(m.services.Setting.Snapshot())
}

func memoryTypeForLevel(level string) string {
	switch level {
	case "global":
		return "User"
	case "local":
		return "Local"
	default:
		return "Project"
	}
}

func joinSections(parts []string) string {
	return strings.Join(parts, "\n\n")
}

func buildHookCompleter(p llm.Provider) hook.LLMCompleter {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, systemPrompt, userMessage, model string) (string, error) {
		c := llm.NewClient(p, model, 0)
		resp, err := c.Complete(ctx, systemPrompt, []core.Message{{
			Role:    core.RoleUser,
			Content: userMessage,
		}}, 4096)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
}
